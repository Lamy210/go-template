package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Lamy210/go-template/internal/config"
	"github.com/Lamy210/go-template/internal/modules/health"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const apiVersion = "0.1.0"

// Server owns the HTTP transport and lifecycle.
type Server struct {
	httpServer *http.Server
	handler    http.Handler
}

// New builds the HTTP server with bounded request sizes and timeout defaults.
func New(cfg config.HTTPConfig, logger *slog.Logger) *Server {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Use(middleware.RequestSize(cfg.MaxBodyBytes))
	router.Use(accessLog(logger))

	apiConfig := huma.DefaultConfig("Go Service API", apiVersion)
	api := humachi.New(router, apiConfig)
	health.Register(api, nil)

	return &Server{
		handler: router,
		httpServer: &http.Server{
			Addr:              cfg.Addr,
			Handler:           router,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			ReadTimeout:       cfg.ReadTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
			MaxHeaderBytes:    cfg.MaxHeaderBytes,
		},
	}
}

// Handler exposes the configured HTTP handler for tests and embedding.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// ListenAndServe starts the configured HTTP server.
func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully drains in-flight HTTP requests.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func accessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(wrapped, r)

			logger.InfoContext(r.Context(), "http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.Status(),
				"bytes", wrapped.BytesWritten(),
				"duration_ms", time.Since(started).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}
