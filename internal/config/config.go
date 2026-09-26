package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultServiceName       = "go-service"
	defaultEnvironment       = "development"
	defaultHTTPAddr          = ":8080"
	defaultLogLevel          = "INFO"
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 30 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 2 * time.Minute
	defaultShutdownTimeout   = 15 * time.Second
	defaultMaxHeaderBytes    = 1 << 20
	defaultMaxBodyBytes      = 1 << 20
)

// Config contains process-wide configuration loaded once at startup.
type Config struct {
	ServiceName string
	Environment string
	LogLevel    string
	HTTP        HTTPConfig
	Database    DatabaseConfig
}

// HTTPConfig contains HTTP server limits and timeout policy.
type HTTPConfig struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxHeaderBytes    int
	MaxBodyBytes      int64
}

// Load reads configuration from environment variables and validates it.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

type lookupEnv func(string) (string, bool)

func load(lookup lookupEnv) (Config, error) {
	readHeaderTimeout, err := durationValue(lookup, "HTTP_READ_HEADER_TIMEOUT", defaultReadHeaderTimeout)
	if err != nil {
		return Config{}, err
	}
	readTimeout, err := durationValue(lookup, "HTTP_READ_TIMEOUT", defaultReadTimeout)
	if err != nil {
		return Config{}, err
	}
	writeTimeout, err := durationValue(lookup, "HTTP_WRITE_TIMEOUT", defaultWriteTimeout)
	if err != nil {
		return Config{}, err
	}
	idleTimeout, err := durationValue(lookup, "HTTP_IDLE_TIMEOUT", defaultIdleTimeout)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := durationValue(lookup, "HTTP_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}
	maxHeaderBytes, err := intValue(lookup, "HTTP_MAX_HEADER_BYTES", defaultMaxHeaderBytes)
	if err != nil {
		return Config{}, err
	}
	maxBodyBytes, err := int64Value(lookup, "HTTP_MAX_BODY_BYTES", defaultMaxBodyBytes)
	if err != nil {
		return Config{}, err
	}
	databaseConfig, err := loadDatabase(lookup)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		ServiceName: stringValue(lookup, "SERVICE_NAME", defaultServiceName),
		Environment: stringValue(lookup, "APP_ENV", defaultEnvironment),
		LogLevel:    strings.ToUpper(stringValue(lookup, "LOG_LEVEL", defaultLogLevel)),
		HTTP: HTTPConfig{
			Addr:              stringValue(lookup, "HTTP_ADDR", defaultHTTPAddr),
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			ShutdownTimeout:   shutdownTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
			MaxBodyBytes:      maxBodyBytes,
		},
		Database: databaseConfig,
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate fails fast on configuration that would result in an unsafe server.
func (c Config) Validate() error {
	if strings.TrimSpace(c.ServiceName) == "" {
		return fmt.Errorf("SERVICE_NAME must not be empty")
	}
	if strings.TrimSpace(c.Environment) == "" {
		return fmt.Errorf("APP_ENV must not be empty")
	}
	if strings.TrimSpace(c.HTTP.Addr) == "" {
		return fmt.Errorf("HTTP_ADDR must not be empty")
	}
	if c.HTTP.ReadHeaderTimeout <= 0 {
		return fmt.Errorf("HTTP_READ_HEADER_TIMEOUT must be positive")
	}
	if c.HTTP.ReadTimeout <= 0 {
		return fmt.Errorf("HTTP_READ_TIMEOUT must be positive")
	}
	if c.HTTP.WriteTimeout <= 0 {
		return fmt.Errorf("HTTP_WRITE_TIMEOUT must be positive")
	}
	if c.HTTP.IdleTimeout <= 0 {
		return fmt.Errorf("HTTP_IDLE_TIMEOUT must be positive")
	}
	if c.HTTP.ShutdownTimeout <= 0 {
		return fmt.Errorf("HTTP_SHUTDOWN_TIMEOUT must be positive")
	}
	if c.HTTP.MaxHeaderBytes <= 0 {
		return fmt.Errorf("HTTP_MAX_HEADER_BYTES must be positive")
	}
	if c.HTTP.MaxBodyBytes <= 0 {
		return fmt.Errorf("HTTP_MAX_BODY_BYTES must be positive")
	}
	if err := c.Database.Validate(); err != nil {
		return err
	}
	return nil
}

func stringValue(lookup lookupEnv, key, fallback string) string {
	value, ok := lookup(key)
	if !ok {
		return fallback
	}
	return value
}

func durationValue(lookup lookupEnv, key string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func intValue(lookup lookupEnv, key string, fallback int) (int, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func int64Value(lookup lookupEnv, key string, fallback int64) (int64, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}
