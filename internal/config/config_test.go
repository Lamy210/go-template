package config

import (
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
	if cfg.Database.Enabled {
		t.Fatal("Database.Enabled = true, want false")
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"SERVICE_NAME":             "example-api",
		"APP_ENV":                  "production",
		"HTTP_ADDR":                "127.0.0.1:9090",
		"LOG_LEVEL":                "debug",
		"HTTP_SHUTDOWN_TIMEOUT":    "3s",
		"HTTP_MAX_HEADER_BYTES":    "2048",
		"HTTP_MAX_BODY_BYTES":      "4096",
		"HTTP_READ_HEADER_TIMEOUT": "2s",
		"DATABASE_ENABLED":         "true",
		"DATABASE_URL":             "postgres://example.invalid/app",
		"DATABASE_MAX_CONNS":       "20",
		"DATABASE_MIN_CONNS":       "2",
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
	if !cfg.Database.Enabled {
		t.Fatal("Database.Enabled = false, want true")
	}
	if cfg.Database.MaxConns != 20 || cfg.Database.MinConns != 2 {
		t.Fatalf("database pool = min:%d max:%d, want min:2 max:20", cfg.Database.MinConns, cfg.Database.MaxConns)
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
			"DATABASE_ENABLED":   "false",
			"DATABASE_MAX_CONNS": "not-an-integer",
			"DATABASE_URL":       "not-a-postgres-url",
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
