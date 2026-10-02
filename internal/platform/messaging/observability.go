package messaging

import (
	"context"

	"github.com/nats-io/nats.go"
)

type operationStarter func(context.Context, string) (context.Context, func(error))

// operationValueContext accepts observability values from a tracer or
// propagator while keeping cancellation and deadlines owned by the business
// context.
type operationValueContext struct {
	context.Context
	values context.Context
}

func (c operationValueContext) Value(key any) (value any) {
	if c.values != nil {
		func() {
			defer func() {
				_ = recover()
			}()
			value = c.values.Value(key)
		}()
		if value != nil {
			return value
		}
	}
	return c.Context.Value(key)
}

func contextWithObservabilityValues(
	ctx context.Context,
	values context.Context,
) context.Context {
	if values == nil {
		return ctx
	}
	return operationValueContext{
		Context: ctx,
		values:  context.WithoutCancel(values),
	}
}

func (c *Client) startPublishOperationSafely(
	ctx context.Context,
	destination string,
) (context.Context, func(error)) {
	if c == nil || c.tracer == nil {
		return ctx, func(error) {}
	}
	return startOperationSafely(ctx, destination, c.tracer.StartPublish)
}

func (c *Client) startProcessOperationSafely(
	ctx context.Context,
	destination string,
) (context.Context, func(error)) {
	if c == nil || c.tracer == nil {
		return ctx, func(error) {}
	}
	return startOperationSafely(ctx, destination, c.tracer.StartProcess)
}

func startOperationSafely(
	ctx context.Context,
	destination string,
	start operationStarter,
) (operationCtx context.Context, end func(error)) {
	operationCtx = ctx
	end = func(error) {}
	if start == nil {
		return operationCtx, end
	}

	defer func() {
		if recover() == nil {
			return
		}
		operationCtx = ctx
		end = func(error) {}
	}()

	startedCtx, finish := start(ctx, destination)
	if startedCtx != nil {
		operationCtx = contextWithObservabilityValues(ctx, startedCtx)
	}
	if finish != nil {
		end = func(err error) {
			finishOperationSafely(finish, err)
		}
	}
	return operationCtx, end
}

func finishOperationSafely(finish func(error), err error) {
	if finish == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	finish(err)
}

func (c *Client) injectPropagationSafely(ctx context.Context, header nats.Header) {
	if c == nil || c.propagator == nil {
		return
	}

	// Stage propagation writes so a hook panic cannot leave a partially mutated
	// outbound header set.
	staged := nats.Header{}
	ok := false
	func() {
		defer func() {
			if recover() != nil {
				return
			}
			ok = true
		}()
		c.propagator.Inject(ctx, natsHeaderCarrier{header: staged})
	}()
	if !ok {
		return
	}

	target := natsHeaderCarrier{header: header}
	stagedCarrier := natsHeaderCarrier{header: staged}
	keys := stagedCarrier.Keys()
	for _, key := range keys {
		if !validNATSHeaderKey(key) {
			// Propagation is optional observability metadata. Discard the
			// complete staged set rather than letting one invalid key make an
			// otherwise valid business message fail NATS header serialization.
			return
		}
		if natsControlHeaderKey(key) {
			// Nats-* headers can control JetStream publish semantics. Optional
			// observability propagation never owns that transport namespace.
			return
		}
		if target.Has(key) {
			// Existing message headers are business/transport state. Never let
			// optional propagation overwrite message identity or quarantine
			// metadata; discard the complete staged set to avoid a mixed trace.
			return
		}
	}
	for _, key := range keys {
		target.Set(key, stagedCarrier.Get(key))
	}
}

func (c *Client) extractPropagationSafely(
	ctx context.Context,
	header nats.Header,
) (result context.Context) {
	result = ctx
	if c == nil || c.propagator == nil {
		return result
	}

	defer func() {
		if recover() != nil {
			result = ctx
		}
	}()
	extracted := c.propagator.Extract(ctx, natsHeaderCarrier{header: header})
	if extracted != nil {
		result = contextWithObservabilityValues(ctx, extracted)
	}
	return result
}

func cloneNATSHeader(header nats.Header) nats.Header {
	clone := make(nats.Header, len(header))
	for key, values := range header {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}
