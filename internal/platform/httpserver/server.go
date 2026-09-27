package httpserver

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	apiVersion              = "0.1.0"
	unmatchedRoute          = "<unmatched>"
	maxClientRequestIDBytes = 128
)

// ContextLogAttrs extracts optional structured access-log fields from a request context.
type ContextLogAttrs func(context.Context) []slog.Attr

// RouteObserver receives the matched low-cardinality route pattern after the
// handler returns. Unknown/unmatched routes are not reported.
type RouteObserver func(context.Context, string, string)

type serverOptions struct {
	contextLogAttrs ContextLogAttrs
	routeObserver   RouteObserver
	outerMiddleware []func(http.Handler) http.Handler
}

// Option configures optional HTTP adapter integrations.
type Option func(*serverOptions)

// WithContextLogAttrs adds context-derived structured fields to access logs.
func WithContextLogAttrs(attrs ContextLogAttrs) Option {
	return func(options *serverOptions) {
		options.contextLogAttrs = attrs
	}
}

// WithRouteObserver observes resolved method/route pairs after request handling.
func WithRouteObserver(observer RouteObserver) Option {
	return func(options *serverOptions) {
		options.routeObserver = observer
	}
}

// WithOuterMiddleware wraps the router from left to right. It is intended for
// cross-cutting middleware that must sit outside the chi router.
func WithOuterMiddleware(middleware ...func(http.Handler) http.Handler) Option {
	return func(options *serverOptions) {
		for _, item := range middleware {
			if item != nil {
				options.outerMiddleware = append(options.outerMiddleware, item)
			}
		}
	}
}

// Server owns the HTTP transport and lifecycle.
type Server struct {
	httpServer *http.Server
	handler    http.Handler
}

// New builds a validated HTTP server from adapter-owned transport settings.
func New(
	cfg Config,
	logger *slog.Logger,
	info ServiceInfo,
	ready ReadinessCheck,
	opts ...Option,
) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		return nil, errors.New("http logger must not be nil")
	}

	var options serverOptions
	for _, option := range opts {
		if option != nil {
			option(&options)
		}
	}

	router := chi.NewRouter()
	router.Use(requestID)
	// Keep the access logger outside panic recovery so recovered panics are
	// recorded as completed 500 responses rather than skipping the post-handler log.
	router.Use(accessLog(logger, options.contextLogAttrs, options.routeObserver))
	router.Use(safeRecoverer(logger))
	router.Use(middleware.RequestSize(cfg.MaxBodyBytes))

	api := humachi.New(router, newAPIConfig())
	registerHealth(api, ready)
	registerVersion(api, info)

	var handler http.Handler = router
	for i := len(options.outerMiddleware) - 1; i >= 0; i-- {
		handler = options.outerMiddleware[i](handler)
	}

	return &Server{
		handler: handler,
		httpServer: &http.Server{
			Addr:              cfg.Addr,
			Handler:           handler,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			ReadTimeout:       cfg.ReadTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
			MaxHeaderBytes:    cfg.MaxHeaderBytes,
		},
	}, nil
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := r.Header.Get(middleware.RequestIDHeader)
		if !validClientRequestID(value) {
			if value != "" {
				r.Header.Del(middleware.RequestIDHeader)
			}
			value = rand.Text()
		}

		w.Header().Set(middleware.RequestIDHeader, value)
		ctx := context.WithValue(r.Context(), middleware.RequestIDKey, value)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func validClientRequestID(value string) bool {
	if value == "" || len(value) > maxClientRequestIDBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func newAPIConfig() huma.Config {
	cfg := huma.DefaultConfig("Go Service API", apiVersion)
	cfg.Transformers = append(cfg.Transformers, transformHumaError)
	return cfg
}

// Handler exposes the configured HTTP handler for tests and embedding.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// ListenAndServe starts the configured HTTP server.
func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully drains in-flight HTTP requests until ctx expires. If the
// graceful deadline is exceeded or canceled, it force-closes active HTTP
// connections before returning so dependency teardown cannot race indefinitely
// with still-connected request handlers.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.httpServer.Shutdown(ctx)
	if err == nil {
		return nil
	}

	ctxErr := ctx.Err()
	if ctxErr == nil || !errors.Is(err, ctxErr) {
		return err
	}

	return errors.Join(err, s.httpServer.Close())
}

func accessLog(
	logger *slog.Logger,
	contextAttrs ContextLogAttrs,
	routeObserver RouteObserver,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(wrapped, r)

			routePattern := chi.RouteContext(r.Context()).RoutePattern()
			if routeObserver != nil && routePattern != "" {
				routeObserver(r.Context(), r.Method, routePattern)
			}

			logRoute := routePattern
			if logRoute == "" {
				logRoute = unmatchedRoute
			}

			status := wrapped.Status()
			if status == 0 {
				status = http.StatusOK
			}

			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("route", logRoute),
				slog.Int("status", status),
				slog.Int("bytes", wrapped.BytesWritten()),
				slog.Int64("duration_ms", time.Since(started).Milliseconds()),
				slog.String("request_id", middleware.GetReqID(r.Context())),
			}
			if contextAttrs != nil {
				attrs = append(attrs, contextAttrs(r.Context())...)
			}
			logger.LogAttrs(r.Context(), slog.LevelInfo, "http request", attrs...)
		})
	}
}
