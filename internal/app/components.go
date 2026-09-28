package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Lamy210/go-template/internal/buildinfo"
	"github.com/Lamy210/go-template/internal/config"
	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
	"github.com/Lamy210/go-template/internal/platform/database"
	"github.com/Lamy210/go-template/internal/platform/httpserver"
	"github.com/Lamy210/go-template/internal/platform/messaging"
	"github.com/Lamy210/go-template/internal/platform/outbox"
	"github.com/Lamy210/go-template/internal/platform/telemetry"
)

type telemetryRuntime struct {
	provider *telemetry.Provider
	options  []httpserver.Option
	shutdown func(context.Context) error
}

func openTelemetry(
	ctx context.Context,
	cfg config.Config,
	info buildinfo.Info,
	logger *slog.Logger,
) (*telemetryRuntime, error) {
	if !cfg.Telemetry.Enabled {
		return nil, nil
	}

	provider, err := telemetry.Open(
		ctx,
		telemetry.Config{
			Endpoint:                cfg.Telemetry.Endpoint,
			ExportTimeout:           cfg.Telemetry.ExportTimeout,
			RetryInitialInterval:    cfg.Telemetry.RetryInitialInterval,
			RetryMaxInterval:        cfg.Telemetry.RetryMaxInterval,
			RetryMaxElapsedTime:     cfg.Telemetry.RetryMaxElapsedTime,
			MetricInterval:          cfg.Telemetry.MetricInterval,
			TraceSampleRatio:        cfg.Telemetry.TraceSampleRatio,
			TraceMaxQueueSize:       cfg.Telemetry.TraceMaxQueueSize,
			TraceMaxExportBatchSize: cfg.Telemetry.TraceMaxExportBatchSize,
			TraceBatchTimeout:       cfg.Telemetry.TraceBatchTimeout,
			MaxExportRequestBytes:   cfg.Telemetry.MaxExportRequestBytes,
		},
		telemetry.ResourceConfig{
			ServiceName: cfg.ServiceName,
			Version:     info.Version,
			Environment: cfg.Environment,
		},
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize telemetry: %w", err)
	}

	logger.Info("opentelemetry traces and metrics enabled",
		"trace_sample_ratio", cfg.Telemetry.TraceSampleRatio,
		"metric_interval", cfg.Telemetry.MetricInterval,
	)

	return &telemetryRuntime{
		provider: provider,
		options: []httpserver.Option{
			httpserver.WithContextLogAttrs(telemetry.LogAttrs),
			httpserver.WithRouteObserver(provider.ObserveHTTPRoute),
			httpserver.WithOuterMiddleware(provider.HTTPMiddleware(cfg.ServiceName)),
		},
		shutdown: provider.Shutdown,
	}, nil
}

type databaseRuntime struct {
	outboxStore *outbox.Store
	readiness   httpserver.ReadinessCheck
	shutdown    func(context.Context) error
}

func openDatabase(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
) (*databaseRuntime, error) {
	if !cfg.Database.Enabled {
		return nil, nil
	}

	pool, err := database.Open(ctx, database.Config{
		URL:               cfg.Database.URL,
		MaxConns:          cfg.Database.MaxConns,
		MinConns:          cfg.Database.MinConns,
		MaxConnLifetime:   cfg.Database.MaxConnLifetime,
		MaxConnIdleTime:   cfg.Database.MaxConnIdleTime,
		HealthCheckPeriod: cfg.Database.HealthCheckPeriod,
		ConnectTimeout:    cfg.Database.ConnectTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize database: %w", err)
	}

	runtime := &databaseRuntime{
		readiness: httpserver.ReadinessCheck(
			database.ReadinessCheck(pool, cfg.Database.HealthTimeout),
		),
		shutdown: func(shutdownCtx context.Context) error {
			return database.Close(shutdownCtx, pool)
		},
	}

	if cfg.Outbox.Enabled {
		runtime.outboxStore, err = outbox.NewStore(pool)
		if err != nil {
			_ = shutdownWithin(cfg.Database.ShutdownTimeout, runtime.shutdown)
			return nil, fmt.Errorf("initialize outbox store: %w", err)
		}
	}

	logger.Info("postgres connection pool ready",
		"max_conns", cfg.Database.MaxConns,
		"min_conns", cfg.Database.MinConns,
	)
	return runtime, nil
}

type messagingRuntime struct {
	client    *messaging.Client
	readiness httpserver.ReadinessCheck
}

func openMessaging(
	ctx context.Context,
	cfg config.Config,
	telemetryRuntime *telemetryRuntime,
	logger *slog.Logger,
) (*messagingRuntime, error) {
	if !cfg.NATS.Enabled {
		return nil, nil
	}

	var options []messaging.Option
	if telemetryRuntime != nil {
		options = append(
			options,
			messaging.WithPropagator(telemetryRuntime.provider),
			messaging.WithTracer(telemetryRuntime.provider),
		)
	}

	client, err := messaging.Open(messaging.ClientConfig{
		URL:            cfg.NATS.URL,
		Name:           cfg.ServiceName,
		ConnectTimeout: cfg.NATS.ConnectTimeout,
		ReconnectWait:  cfg.NATS.ReconnectWait,
		MaxReconnects:  cfg.NATS.MaxReconnects,
		DrainTimeout:   cfg.NATS.DrainTimeout,
		RequestTimeout: cfg.NATS.RequestTimeout,
	}, options...)
	if err != nil {
		return nil, fmt.Errorf("initialize nats: %w", err)
	}

	streamConfig := messaging.StreamConfig{
		Name:            cfg.NATS.Stream,
		Subjects:        cfg.NATS.Subjects,
		MaxConsumers:    cfg.NATS.MaxConsumers,
		MaxMessages:     cfg.NATS.MaxMessages,
		MaxBytes:        cfg.NATS.MaxBytes,
		MaxAge:          cfg.NATS.MaxAge,
		MaxMessageSize:  cfg.NATS.MaxMessageSize,
		DuplicateWindow: cfg.NATS.DuplicateWindow,
	}
	if err := client.EnsureStream(ctx, streamConfig); err != nil {
		client.Close()
		return nil, fmt.Errorf("initialize jetstream stream: %w", err)
	}

	logger.Info("nats jetstream ready",
		"stream", cfg.NATS.Stream,
		"max_reconnects", cfg.NATS.MaxReconnects,
	)

	return &messagingRuntime{
		client: client,
		readiness: httpserver.ReadinessCheck(
			client.ReadinessCheck(streamConfig, cfg.NATS.RequestTimeout),
		),
	}, nil
}

func newOutboxDispatcher(
	cfg config.Config,
	store *outbox.Store,
	messagingRuntime *messagingRuntime,
	telemetryRuntime *telemetryRuntime,
) (*outbox.Dispatcher, error) {
	if !cfg.Outbox.Enabled {
		return nil, nil
	}
	if store == nil {
		return nil, errors.New("initialize outbox dispatcher: outbox store is unavailable")
	}
	if messagingRuntime == nil || messagingRuntime.client == nil {
		return nil, errors.New("initialize outbox dispatcher: messaging client is unavailable")
	}

	var propagator coreprop.TextMapPropagator
	if telemetryRuntime != nil {
		propagator = telemetryRuntime.provider
	}

	dispatcher, err := outbox.NewDispatcher(
		store,
		func(publishCtx context.Context, subject, eventID string, payload []byte) error {
			_, err := messagingRuntime.client.Publish(publishCtx, subject, eventID, payload)
			return err
		},
		propagator,
		outbox.DispatcherConfig{
			BatchSize:      cfg.Outbox.BatchSize,
			PollInterval:   cfg.Outbox.PollInterval,
			Lease:          cfg.Outbox.Lease,
			MaxAttempts:    cfg.Outbox.MaxAttempts,
			RetryBaseDelay: cfg.Outbox.RetryBaseDelay,
			RetryMaxDelay:  cfg.Outbox.RetryMaxDelay,
			PublishTimeout: cfg.Outbox.PublishTimeout,
			StoreTimeout:   cfg.Outbox.StoreTimeout,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("initialize outbox dispatcher: %w", err)
	}
	return dispatcher, nil
}

func serviceInfo(cfg config.Config, info buildinfo.Info) httpserver.ServiceInfo {
	return httpserver.ServiceInfo{
		Service:   cfg.ServiceName,
		Version:   info.Version,
		Commit:    info.Commit,
		BuildTime: info.BuildDate,
	}
}

func httpConfig(cfg config.HTTPConfig) httpserver.Config {
	return httpserver.Config{
		Addr:              cfg.Addr,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		MaxBodyBytes:      cfg.MaxBodyBytes,
	}
}

func shutdownWithin(
	timeout time.Duration,
	shutdown func(context.Context) error,
) error {
	if shutdown == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return shutdown(ctx)
}
