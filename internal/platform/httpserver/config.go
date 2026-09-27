package httpserver

import (
	"errors"
	"strings"
	"time"
)

// Config contains the HTTP transport limits and timeout policy owned by the
// server adapter. Process-wide shutdown budgeting remains at the application
// composition boundary.
type Config struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	MaxBodyBytes      int64
}

// Validate rejects server settings that would remove transport bounds or create
// an unusable listener.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Addr) == "" {
		return errors.New("http address must not be empty")
	}
	if c.ReadHeaderTimeout <= 0 {
		return errors.New("http read header timeout must be positive")
	}
	if c.ReadTimeout <= 0 {
		return errors.New("http read timeout must be positive")
	}
	if c.WriteTimeout <= 0 {
		return errors.New("http write timeout must be positive")
	}
	if c.IdleTimeout <= 0 {
		return errors.New("http idle timeout must be positive")
	}
	if c.MaxHeaderBytes <= 0 {
		return errors.New("http max header bytes must be positive")
	}
	if c.MaxBodyBytes <= 0 {
		return errors.New("http max body bytes must be positive")
	}
	return nil
}
