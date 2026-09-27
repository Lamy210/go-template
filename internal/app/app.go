package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/Lamy210/go-template/internal/buildinfo"
	"github.com/Lamy210/go-template/internal/config"
	"github.com/Lamy210/go-template/internal/platform/database"
	"github.com/Lamy210/go-template/internal/platform/httpserver"
)

// Run loads configuration, initializes dependencies, starts the HTTP server,
// and performs a graceful shutdown when the context is canceled.
func Run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	info := buildinfo.Current()
	logger, err := newLogger(os.Stdout, cfg.LogLevel, cfg.ServiceName, cfg.Environment, info)
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}

	serviceInfo := httpserver.ServiceInfo{
		Service:   cfg.ServiceName,
		Version:   info.Version,
		Commit:    info.Commit,
		BuildTime: info.BuildDate,
	}

	var ready httpserver.ReadinessCheck
	if cfg.Database.Enabled {
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
			return fmt.Errorf("initialize database: %w", err)
		}
		defer pool.Close()

		ready = httpserver.ReadinessCheck(database.ReadinessCheck(pool, cfg.Database.HealthTimeout))
		logger.Info("postgres connection pool ready",
			"max_conns", cfg.Database.MaxConns,
			"min_conns", cfg.Database.MinConns,
		)
	}

	server := httpserver.New(cfg.HTTP, logger, serviceInfo, ready)
	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("serve http: %w", err)
		}
	}()

	logger.Info("http server started",
		"addr", cfg.HTTP.Addr,
		"commit", info.Commit,
		"build_date", info.BuildDate,
	)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown requested")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}

	logger.Info("shutdown complete")
	return nil
}

func newLogger(
	out io.Writer,
	levelText string,
	serviceName string,
	environment string,
	info buildinfo.Info,
) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(levelText)); err != nil {
		return nil, fmt.Errorf("parse log level %q: %w", levelText, err)
	}

	logger := slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level}))
	return logger.With(
		"service", serviceName,
		"version", info.Version,
		"environment", environment,
	), nil
}
