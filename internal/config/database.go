package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	defaultDatabaseEnabled           = false
	defaultDatabaseMaxConns          = int32(10)
	defaultDatabaseMinConns          = int32(0)
	defaultDatabaseMaxConnLifetime   = 30 * time.Minute
	defaultDatabaseMaxConnIdleTime   = 5 * time.Minute
	defaultDatabaseHealthCheckPeriod = time.Minute
	defaultDatabaseConnectTimeout    = 5 * time.Second
	defaultDatabaseHealthTimeout     = 2 * time.Second
)

// DatabaseConfig contains optional PostgreSQL profile configuration.
type DatabaseConfig struct {
	Enabled           bool
	URL               string
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	ConnectTimeout    time.Duration
	HealthTimeout     time.Duration
}

func loadDatabase(lookup lookupEnv) (DatabaseConfig, error) {
	enabled, err := boolValue(lookup, "DATABASE_ENABLED", defaultDatabaseEnabled)
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxConns, err := int32Value(lookup, "DATABASE_MAX_CONNS", defaultDatabaseMaxConns)
	if err != nil {
		return DatabaseConfig{}, err
	}
	minConns, err := int32Value(lookup, "DATABASE_MIN_CONNS", defaultDatabaseMinConns)
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxConnLifetime, err := durationValue(lookup, "DATABASE_MAX_CONN_LIFETIME", defaultDatabaseMaxConnLifetime)
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxConnIdleTime, err := durationValue(lookup, "DATABASE_MAX_CONN_IDLE_TIME", defaultDatabaseMaxConnIdleTime)
	if err != nil {
		return DatabaseConfig{}, err
	}
	healthCheckPeriod, err := durationValue(lookup, "DATABASE_HEALTH_CHECK_PERIOD", defaultDatabaseHealthCheckPeriod)
	if err != nil {
		return DatabaseConfig{}, err
	}
	connectTimeout, err := durationValue(lookup, "DATABASE_CONNECT_TIMEOUT", defaultDatabaseConnectTimeout)
	if err != nil {
		return DatabaseConfig{}, err
	}
	healthTimeout, err := durationValue(lookup, "DATABASE_HEALTH_TIMEOUT", defaultDatabaseHealthTimeout)
	if err != nil {
		return DatabaseConfig{}, err
	}

	cfg := DatabaseConfig{
		Enabled:           enabled,
		URL:               stringValue(lookup, "DATABASE_URL", ""),
		MaxConns:          maxConns,
		MinConns:          minConns,
		MaxConnLifetime:   maxConnLifetime,
		MaxConnIdleTime:   maxConnIdleTime,
		HealthCheckPeriod: healthCheckPeriod,
		ConnectTimeout:    connectTimeout,
		HealthTimeout:     healthTimeout,
	}
	if err := cfg.Validate(); err != nil {
		return DatabaseConfig{}, err
	}
	return cfg, nil
}

// Validate verifies PostgreSQL pool settings without logging the database URL.
func (c DatabaseConfig) Validate() error {
	if c.Enabled && strings.TrimSpace(c.URL) == "" {
		return fmt.Errorf("DATABASE_URL must not be empty when DATABASE_ENABLED=true")
	}
	if c.MaxConns <= 0 {
		return fmt.Errorf("DATABASE_MAX_CONNS must be positive")
	}
	if c.MinConns < 0 {
		return fmt.Errorf("DATABASE_MIN_CONNS must not be negative")
	}
	if c.MinConns > c.MaxConns {
		return fmt.Errorf("DATABASE_MIN_CONNS must not exceed DATABASE_MAX_CONNS")
	}
	if c.MaxConnLifetime <= 0 {
		return fmt.Errorf("DATABASE_MAX_CONN_LIFETIME must be positive")
	}
	if c.MaxConnIdleTime <= 0 {
		return fmt.Errorf("DATABASE_MAX_CONN_IDLE_TIME must be positive")
	}
	if c.HealthCheckPeriod <= 0 {
		return fmt.Errorf("DATABASE_HEALTH_CHECK_PERIOD must be positive")
	}
	if c.ConnectTimeout <= 0 {
		return fmt.Errorf("DATABASE_CONNECT_TIMEOUT must be positive")
	}
	if c.HealthTimeout <= 0 {
		return fmt.Errorf("DATABASE_HEALTH_TIMEOUT must be positive")
	}
	return nil
}

func boolValue(lookup lookupEnv, key string, fallback bool) (bool, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func int32Value(lookup lookupEnv, key string, fallback int32) (int32, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return int32(parsed), nil
}
