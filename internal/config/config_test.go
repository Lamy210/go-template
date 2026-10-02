package config

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}

	if cfg.ServiceName != defaultServiceName {
		t.Fatalf("ServiceName = %q, want %q", cfg.ServiceName, defaultServiceName)
	}
	if cfg.Environment != defaultEnvironment {
		t.Fatalf("Environment = %q, want %q", cfg.Environment, defaultEnvironment)
	}
	if cfg.HTTP.Addr != defaultHTTPAddr {
		t.Fatalf("HTTP.Addr = %q, want %q", cfg.HTTP.Addr, defaultHTTPAddr)
	}
	if cfg.HTTP.ReadHeaderTimeout != defaultReadHeaderTimeout {
		t.Fatalf("HTTP.ReadHeaderTimeout = %s, want %s", cfg.HTTP.ReadHeaderTimeout, defaultReadHeaderTimeout)
	}
	if !cfg.HTTP.DocsEnabled {
		t.Fatal("HTTP.DocsEnabled = false, want true")
	}
	if cfg.Database.Enabled {
		t.Fatal("Database.Enabled = true, want false")
	}
	if cfg.NATS.Enabled {
		t.Fatal("NATS.Enabled = true, want false")
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"SERVICE_NAME":              "example-api",
		"APP_ENV":                   "production",
		"HTTP_ADDR":                 "127.0.0.1:9090",
		"LOG_LEVEL":                 "debug",
		"HTTP_SHUTDOWN_TIMEOUT":     "3s",
		"HTTP_MAX_HEADER_BYTES":     "2048",
		"HTTP_MAX_BODY_BYTES":       "4096",
		"HTTP_READ_HEADER_TIMEOUT":  "2s",
		"HTTP_DOCS_ENABLED":         "false",
		"DATABASE_ENABLED":          "true",
		"DATABASE_URL":              "postgres://example.invalid/app",
		"DATABASE_MAX_CONNS":        "20",
		"DATABASE_MIN_CONNS":        "2",
		"DATABASE_SHUTDOWN_TIMEOUT": "7s",
	}

	cfg, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("load overrides: %v", err)
	}

	if cfg.ServiceName != "example-api" {
		t.Fatalf("ServiceName = %q, want example-api", cfg.ServiceName)
	}
	if cfg.LogLevel != "DEBUG" {
		t.Fatalf("LogLevel = %q, want DEBUG", cfg.LogLevel)
	}
	if cfg.HTTP.ShutdownTimeout != 3*time.Second {
		t.Fatalf("ShutdownTimeout = %s, want 3s", cfg.HTTP.ShutdownTimeout)
	}
	if cfg.HTTP.MaxHeaderBytes != 2048 {
		t.Fatalf("MaxHeaderBytes = %d, want 2048", cfg.HTTP.MaxHeaderBytes)
	}
	if cfg.HTTP.MaxBodyBytes != 4096 {
		t.Fatalf("MaxBodyBytes = %d, want 4096", cfg.HTTP.MaxBodyBytes)
	}
	if cfg.HTTP.DocsEnabled {
		t.Fatal("HTTP.DocsEnabled = true, want false")
	}
	if !cfg.Database.Enabled {
		t.Fatal("Database.Enabled = false, want true")
	}
	if cfg.Database.MaxConns != 20 || cfg.Database.MinConns != 2 {
		t.Fatalf("database pool = min:%d max:%d, want min:2 max:20", cfg.Database.MinConns, cfg.Database.MaxConns)
	}
	if cfg.Database.ShutdownTimeout != 7*time.Second {
		t.Fatalf(
			"Database.ShutdownTimeout = %s, want 7s",
			cfg.Database.ShutdownTimeout,
		)
	}
}

func TestLoadRejectsInvalidLogLevel(t *testing.T) {
	t.Parallel()

	_, err := load(func(key string) (string, bool) {
		if key == "LOG_LEVEL" {
			return "trace", true
		}
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Fatalf("load error = %v, want LOG_LEVEL validation error", err)
	}
}

func TestLoadAcceptsSlogLevelOffset(t *testing.T) {
	t.Parallel()

	cfg, err := load(func(key string) (string, bool) {
		if key == "LOG_LEVEL" {
			return "debug+2", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("load error = %v", err)
	}
	if cfg.LogLevel != "DEBUG+2" {
		t.Fatalf("LogLevel = %q, want DEBUG+2", cfg.LogLevel)
	}
}

func TestLoadRejectsInvalidHTTPDocsFlag(t *testing.T) {
	t.Parallel()

	_, err := load(func(key string) (string, bool) {
		if key == "HTTP_DOCS_ENABLED" {
			return "not-a-bool", true
		}
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP_DOCS_ENABLED") {
		t.Fatalf("load error = %v, want HTTP_DOCS_ENABLED parse error", err)
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	t.Parallel()

	_, err := load(func(key string) (string, bool) {
		if key == "HTTP_READ_TIMEOUT" {
			return "not-a-duration", true
		}
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP_READ_TIMEOUT") {
		t.Fatalf("load error = %v, want HTTP_READ_TIMEOUT parse error", err)
	}
}

func TestDisabledDatabaseIgnoresDatabaseSpecificValues(t *testing.T) {
	t.Parallel()

	cfg, err := load(func(key string) (string, bool) {
		values := map[string]string{
			"DATABASE_ENABLED":          "false",
			"DATABASE_MAX_CONNS":        "not-an-integer",
			"DATABASE_URL":              "not-a-postgres-url",
			"DATABASE_SHUTDOWN_TIMEOUT": "not-a-duration",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("load disabled database config: %v", err)
	}
	if cfg.Database.Enabled {
		t.Fatal("Database.Enabled = true, want false")
	}
	if cfg.Database.MaxConns != defaultDatabaseMaxConns {
		t.Fatalf("Database.MaxConns = %d, want default %d", cfg.Database.MaxConns, defaultDatabaseMaxConns)
	}
}

func TestDisabledNATSIgnoresNATSSpecificValues(t *testing.T) {
	t.Parallel()

	cfg, err := load(func(key string) (string, bool) {
		values := map[string]string{
			"NATS_ENABLED":          "false",
			"NATS_MAX_RECONNECTS":   "-1",
			"NATS_STREAM_MAX_BYTES": "not-an-integer",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("load disabled NATS config: %v", err)
	}
	if cfg.NATS.Enabled {
		t.Fatal("NATS.Enabled = true, want false")
	}
	if cfg.NATS.MaxReconnects != defaultNATSMaxReconnects {
		t.Fatalf("NATS.MaxReconnects = %d, want default %d", cfg.NATS.MaxReconnects, defaultNATSMaxReconnects)
	}
}

func TestLoadRejectsUnboundedNATSReconnects(t *testing.T) {
	t.Parallel()

	_, err := load(func(key string) (string, bool) {
		values := map[string]string{
			"NATS_ENABLED":        "true",
			"NATS_MAX_RECONNECTS": "-1",
		}
		value, ok := values[key]
		return value, ok
	})
	if err == nil || !strings.Contains(err.Error(), "NATS_MAX_RECONNECTS") {
		t.Fatalf("load error = %v, want NATS_MAX_RECONNECTS validation error", err)
	}
}

func TestLoadRejectsNATSAckBudgetWithoutSettlementSlack(t *testing.T) {
	t.Parallel()

	_, err := load(func(key string) (string, bool) {
		values := map[string]string{
			"NATS_ENABLED":         "true",
			"NATS_ACK_WAIT":        "5s",
			"NATS_HANDLER_TIMEOUT": "4s",
			"NATS_ACK_TIMEOUT":     "1s",
		}
		value, ok := values[key]
		return value, ok
	})
	if err == nil || !strings.Contains(err.Error(), "NATS_ACK_WAIT") {
		t.Fatalf("load error = %v, want NATS_ACK_WAIT budget validation error", err)
	}
}

func TestLoadRejectsEnabledDatabaseWithoutURL(t *testing.T) {
	t.Parallel()

	_, err := load(func(key string) (string, bool) {
		if key == "DATABASE_ENABLED" {
			return "true", true
		}
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("load error = %v, want DATABASE_URL validation error", err)
	}
}

func TestLoadRejectsNonPositiveDatabaseShutdownTimeout(t *testing.T) {
	t.Parallel()

	_, err := load(func(key string) (string, bool) {
		values := map[string]string{
			"DATABASE_ENABLED":          "true",
			"DATABASE_URL":              "postgres://example.invalid/app",
			"DATABASE_SHUTDOWN_TIMEOUT": "0s",
		}
		value, ok := values[key]
		return value, ok
	})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_SHUTDOWN_TIMEOUT") {
		t.Fatalf(
			"load error = %v, want DATABASE_SHUTDOWN_TIMEOUT validation error",
			err,
		)
	}
}

func TestValidateRejectsEmptyServiceName(t *testing.T) {
	t.Parallel()

	cfg, err := load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	cfg.ServiceName = " "

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}

func TestValidateRejectsInvalidUTF8ProcessIdentity(t *testing.T) {
	t.Parallel()

	base, err := load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "service name",
			mutate: func(cfg *Config) {
				cfg.ServiceName = "service-" + string([]byte{0xff})
			},
		},
		{
			name: "environment",
			mutate: func(cfg *Config) {
				cfg.Environment = "prod-" + string([]byte{0xc3, 0x28})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := base
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want invalid UTF-8 identity error")
			}
		})
	}
}

func TestValidateRejectsUnsafeLimits(t *testing.T) {
	t.Parallel()

	cfg, err := load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	cfg.HTTP.MaxBodyBytes = 0

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}

func TestNATSValidateRejectsRuntimeIncompatibleConsumerBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*NATSConfig)
	}{
		{
			name: "filter equals quarantine",
			mutate: func(cfg *NATSConfig) {
				cfg.FilterSubject = cfg.QuarantineSubject
			},
		},
		{
			name: "invalid stream name",
			mutate: func(cfg *NATSConfig) {
				cfg.Stream = "APP.EVENTS"
			},
		},
		{
			name: "invalid UTF-8 stream name",
			mutate: func(cfg *NATSConfig) {
				cfg.Stream = string([]byte{0xff})
			},
		},
		{
			name: "non-printable stream name",
			mutate: func(cfg *NATSConfig) {
				cfg.Stream = "APP\x00EVENTS"
			},
		},
		{
			name: "Unicode whitespace stream name",
			mutate: func(cfg *NATSConfig) {
				cfg.Stream = "APP\u00a0EVENTS"
			},
		},
		{
			name: "invalid durable name",
			mutate: func(cfg *NATSConfig) {
				cfg.Durable = "app worker"
			},
		},
		{
			name: "invalid UTF-8 durable name",
			mutate: func(cfg *NATSConfig) {
				cfg.Durable = string([]byte{0xff})
			},
		},
		{
			name: "non-printable durable name",
			mutate: func(cfg *NATSConfig) {
				cfg.Durable = "worker\vname"
			},
		},
		{
			name: "filter wildcard captures quarantine",
			mutate: func(cfg *NATSConfig) {
				cfg.FilterSubject = "app.events.>"
			},
		},
		{
			name: "quarantine subject contains wildcard",
			mutate: func(cfg *NATSConfig) {
				cfg.QuarantineSubject = "app.events.*"
			},
		},
		{
			name: "invalid stream subject pattern",
			mutate: func(cfg *NATSConfig) {
				cfg.Subjects = []string{"app.events.>.invalid"}
			},
		},
		{
			name: "invalid UTF-8 stream subject",
			mutate: func(cfg *NATSConfig) {
				cfg.Subjects = []string{"app.events." + string([]byte{0xff})}
			},
		},
		{
			name: "invalid UTF-8 filter subject",
			mutate: func(cfg *NATSConfig) {
				cfg.FilterSubject = "app.events." + string([]byte{0xff})
			},
		},
		{
			name: "invalid UTF-8 quarantine subject",
			mutate: func(cfg *NATSConfig) {
				cfg.QuarantineSubject = "app.events." + string([]byte{0xff})
			},
		},
		{
			name: "pull expiry below library minimum",
			mutate: func(cfg *NATSConfig) {
				cfg.PullExpiry = 500 * time.Millisecond
			},
		},
		{
			name: "delivery attempt overflow",
			mutate: func(cfg *NATSConfig) {
				cfg.ProcessAttempts = math.MaxInt
				cfg.QuarantineAttempts = 2
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultNATSConfig()
			cfg.Enabled = true
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want runtime-parity error")
			}
		})
	}
}
