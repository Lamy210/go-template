package httpserver

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "empty address",
			mutate: func(cfg *Config) {
				cfg.Addr = " "
			},
		},
		{
			name: "non-positive read header timeout",
			mutate: func(cfg *Config) {
				cfg.ReadHeaderTimeout = 0
			},
		},
		{
			name: "non-positive read timeout",
			mutate: func(cfg *Config) {
				cfg.ReadTimeout = 0
			},
		},
		{
			name: "non-positive write timeout",
			mutate: func(cfg *Config) {
				cfg.WriteTimeout = 0
			},
		},
		{
			name: "non-positive idle timeout",
			mutate: func(cfg *Config) {
				cfg.IdleTimeout = 0
			},
		},
		{
			name: "non-positive max header bytes",
			mutate: func(cfg *Config) {
				cfg.MaxHeaderBytes = 0
			},
		},
		{
			name: "non-positive max body bytes",
			mutate: func(cfg *Config) {
				cfg.MaxBodyBytes = 0
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func TestNewRejectsInvalidBoundaryInputs(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := testConfig()
	cfg.MaxBodyBytes = 0
	if _, err := New(cfg, logger, testServiceInfo(), nil); err == nil {
		t.Fatal("New() invalid config error = nil")
	}

	if _, err := New(testConfig(), nil, testServiceInfo(), nil); err == nil {
		t.Fatal("New() nil logger error = nil")
	}

	invalidInfo := testServiceInfo()
	invalidInfo.Version = string([]byte{0xff})
	if _, err := New(testConfig(), logger, invalidInfo, nil); err == nil {
		t.Fatal("New() invalid service metadata error = nil")
	}

	if _, err := New(
		testConfig(),
		logger,
		testServiceInfo(),
		nil,
		WithOuterMiddleware(func(http.Handler) http.Handler { return nil }),
	); err == nil {
		t.Fatal("New() nil outer middleware output error = nil")
	}

	const sensitive = "sensitive outer middleware panic"
	_, err := New(
		testConfig(),
		logger,
		testServiceInfo(),
		nil,
		WithOuterMiddleware(func(http.Handler) http.Handler {
			panic(sensitive)
		}),
	)
	if !errors.Is(err, errOuterMiddlewarePanic) {
		t.Fatalf("New() error = %v, want outer middleware panic sentinel", err)
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("New() exposed outer middleware panic value: %q", err.Error())
	}
}

func TestConfigAllowsBoundedServerPolicy(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Addr:              "127.0.0.1:8080",
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      2 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    64 << 10,
		MaxBodyBytes:      1 << 20,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
