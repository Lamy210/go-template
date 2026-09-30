package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
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

	var databaseShutdown func(context.Context) error
	var outboxStore *outbox.Store
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

		databaseShutdown = func(shutdownCtx context.Context) error {
			return database.Close(shutdownCtx, pool)
		}
		defer func() {
			if databaseShutdown == nil {
				return
			}
			shutdown := databaseShutdown
			databaseShutdown = nil

			shutdownCtx, cancel := context.WithTimeout(
				context.Background(),
				cfg.Database.ShutdownTimeout,
			)
			defer cancel()
			if err := shutdown(shutdownCtx); err != nil {
				logger.Warn("database shutdown failed")
			}
		}()

		if cfg.Outbox.Enabled {
			outboxStore, err = outbox.NewStore(pool)
			if err != nil {
				return fmt.Errorf("initialize outbox store: %w", err)
			}
		}

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

	var outboxDispatcher *outbox.Dispatcher
	var outboxCancel context.CancelFunc
	var outboxErrCh <-chan error
	outboxStopped := false
	if cfg.Outbox.Enabled {
		var propagator coreprop.TextMapPropagator
		if telemetryProvider != nil {
			propagator = telemetryProvider
		}

		dispatcher, err := outbox.NewDispatcher(
			outboxStore,
			func(publishCtx context.Context, subject, eventID string, payload []byte) error {
				_, err := natsClient.Publish(publishCtx, subject, eventID, payload)
				return classifyOutboxPublishError(err)
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
			return fmt.Errorf("initialize outbox dispatcher: %w", err)
		}

		outboxDispatcher = dispatcher
	}

	server, err := httpserver.New(
		httpserver.Config{
			Addr:              cfg.HTTP.Addr,
			ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
			ReadTimeout:       cfg.HTTP.ReadTimeout,
			WriteTimeout:      cfg.HTTP.WriteTimeout,
			IdleTimeout:       cfg.HTTP.IdleTimeout,
			MaxHeaderBytes:    cfg.HTTP.MaxHeaderBytes,
			MaxBodyBytes:      cfg.HTTP.MaxBodyBytes,
			DocsEnabled:       cfg.HTTP.DocsEnabled,
		},
		logger,
		serviceInfo,
		combineReadiness(readinessChecks...),
		httpOptions...,
	)
	if err != nil {
		return fmt.Errorf("initialize http server: %w", err)
	}

	listener, err := server.Listen(ctx)
	if err != nil {
		return fmt.Errorf("bind http listener: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("serve http: %w", err)
		}
	}()

	if outboxDispatcher != nil {
		dispatchCtx, cancelOutbox := context.WithCancel(context.WithoutCancel(ctx))
		outboxCancel = cancelOutbox
		runCh := make(chan error, 1)
		outboxErrCh = runCh
		go func() {
			runCh <- outboxDispatcher.Run(dispatchCtx)
		}()

		logger.Info("transactional outbox dispatcher started",
			"batch_size", cfg.Outbox.BatchSize,
			"max_attempts", cfg.Outbox.MaxAttempts,
		)
	}

	logger.Info("http server started",
		"addr", listener.Addr().String(),
		"commit", info.Commit,
		"build_date", info.BuildDate,
	)

	var runErr error
	select {
	case err := <-errCh:
		runErr = err
	case err := <-outboxErrCh:
		outboxStopped = true
		if err != nil {
			runErr = fmt.Errorf("run outbox dispatcher: %w", err)
		} else {
			runErr = errors.New("outbox dispatcher stopped unexpectedly")
		}
	case <-ctx.Done():
		logger.Info("shutdown requested")
	}

	var shutdownErr error

	httpShutdownCtx, cancelHTTP := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	if err := server.Shutdown(httpShutdownCtx); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown http server: %w", err))
	}
	cancelHTTP()

	if outboxCancel != nil {
		outboxCancel()
		if !outboxStopped {
			timer := time.NewTimer(cfg.Outbox.ShutdownTimeout)
			select {
			case err := <-outboxErrCh:
				if err != nil {
					shutdownErr = errors.Join(
						shutdownErr,
						fmt.Errorf("shutdown outbox dispatcher: %w", err),
					)
				}
			case <-timer.C:
				shutdownErr = errors.Join(
					shutdownErr,
					errors.New("shutdown outbox dispatcher: deadline exceeded"),
				)
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
	}

	if natsClient != nil {
		natsDrainCtx, cancelNATS := context.WithTimeout(context.Background(), cfg.NATS.DrainTimeout)
		if err := natsClient.Drain(natsDrainCtx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("drain nats: %w", err))
		}
		cancelNATS()
	}

	if databaseShutdown != nil {
		shutdown := databaseShutdown
		databaseShutdown = nil

		databaseShutdownCtx, cancelDatabase := context.WithTimeout(
			context.Background(),
			cfg.Database.ShutdownTimeout,
		)
		if err := shutdown(databaseShutdownCtx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown database: %w", err))
		}
		cancelDatabase()
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

	if runErr != nil || shutdownErr != nil {
		return errors.Join(runErr, shutdownErr)
	}

	logger.Info("shutdown complete")
	return nil
}

func classifyOutboxPublishError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, messaging.ErrInvalidPublishSubject) ||
		errors.Is(err, messaging.ErrInvalidMessageID) {
		return outbox.MarkPermanentPublishFailure(err)
	}
	return err
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
