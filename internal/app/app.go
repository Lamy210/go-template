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
	"github.com/Lamy210/go-template/internal/platform/messaging"
	"github.com/Lamy210/go-template/internal/platform/telemetry"
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

	var readinessChecks []httpserver.ReadinessCheck
	var httpOptions []httpserver.Option

	var telemetryProvider *telemetry.Provider
	telemetryShutdown := false
	if cfg.Telemetry.Enabled {
		telemetryProvider, err = telemetry.Open(
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
			return fmt.Errorf("initialize telemetry: %w", err)
		}
		defer func() {
			if telemetryShutdown {
				return
			}
			shutdownCtx, cancel := context.WithTimeout(
				context.Background(),
				cfg.Telemetry.ShutdownTimeout,
			)
			defer cancel()
			if err := telemetryProvider.Shutdown(shutdownCtx); err != nil {
				logger.Warn("telemetry shutdown failed")
			}
		}()

		httpOptions = append(
			httpOptions,
			httpserver.WithContextLogAttrs(telemetry.LogAttrs),
			httpserver.WithRouteObserver(telemetryProvider.ObserveHTTPRoute),
			httpserver.WithOuterMiddleware(
				telemetryProvider.HTTPMiddleware(cfg.ServiceName),
			),
		)
		logger.Info("opentelemetry traces and metrics enabled",
			"trace_sample_ratio", cfg.Telemetry.TraceSampleRatio,
			"metric_interval", cfg.Telemetry.MetricInterval,
		)
	}

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

		readinessChecks = append(
			readinessChecks,
			httpserver.ReadinessCheck(database.ReadinessCheck(pool, cfg.Database.HealthTimeout)),
		)
		logger.Info("postgres connection pool ready",
			"max_conns", cfg.Database.MaxConns,
			"min_conns", cfg.Database.MinConns,
		)
	}

	var natsClient *messaging.Client
	if cfg.NATS.Enabled {
		var messagingOptions []messaging.Option
		if telemetryProvider != nil {
			messagingOptions = append(
				messagingOptions,
				messaging.WithPropagator(telemetryProvider),
				messaging.WithTracer(telemetryProvider),
			)
		}

		natsClient, err = messaging.Open(messaging.ClientConfig{
			URL:            cfg.NATS.URL,
			Name:           cfg.ServiceName,
			ConnectTimeout: cfg.NATS.ConnectTimeout,
			ReconnectWait:  cfg.NATS.ReconnectWait,
			MaxReconnects:  cfg.NATS.MaxReconnects,
			DrainTimeout:   cfg.NATS.DrainTimeout,
			RequestTimeout: cfg.NATS.RequestTimeout,
		}, messagingOptions...)
		if err != nil {
			return fmt.Errorf("initialize nats: %w", err)
		}
		defer natsClient.Close()

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
		if err := natsClient.EnsureStream(ctx, streamConfig); err != nil {
			return fmt.Errorf("initialize jetstream stream: %w", err)
		}

		readinessChecks = append(
			readinessChecks,
			httpserver.ReadinessCheck(
				natsClient.ReadinessCheck(streamConfig, cfg.NATS.RequestTimeout),
			),
		)
		logger.Info("nats jetstream ready",
			"stream", cfg.NATS.Stream,
			"max_reconnects", cfg.NATS.MaxReconnects,
		)
	}

	server := httpserver.New(
		cfg.HTTP,
		logger,
		serviceInfo,
		combineReadiness(readinessChecks...),
		httpOptions...,
	)
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

	var shutdownErr error

	httpShutdownCtx, cancelHTTP := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	if err := server.Shutdown(httpShutdownCtx); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown http server: %w", err))
	}
	cancelHTTP()

	if natsClient != nil {
		natsDrainCtx, cancelNATS := context.WithTimeout(context.Background(), cfg.NATS.DrainTimeout)
		if err := natsClient.Drain(natsDrainCtx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("drain nats: %w", err))
		}
		cancelNATS()
	}

	if telemetryProvider != nil {
		telemetryShutdownCtx, cancelTelemetry := context.WithTimeout(
			context.Background(),
			cfg.Telemetry.ShutdownTimeout,
		)
		if err := telemetryProvider.Shutdown(telemetryShutdownCtx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown telemetry: %w", err))
		}
		telemetryShutdown = true
		cancelTelemetry()
	}

	if shutdownErr != nil {
		return shutdownErr
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
