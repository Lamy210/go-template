// Package buildinfo exposes immutable build metadata injected at link time.
package buildinfo

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// Info describes the running binary build.
type Info struct {
	Version   string
	Commit    string
	BuildDate string
}

// Validate keeps linker-injected process identity on one stable text contract
// before it reaches structured logs, HTTP JSON, or telemetry resources.
func (i Info) Validate() error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "version", value: i.Version},
		{name: "commit", value: i.Commit},
		{name: "build date", value: i.BuildDate},
	} {
		if strings.TrimSpace(field.value) == "" {
			return errors.New("build " + field.name + " must not be empty")
		}
		if !utf8.ValidString(field.value) {
			return errors.New("build " + field.name + " must be valid UTF-8")
		}
	}
	return nil
}

// Current returns the build metadata embedded in the running binary.
func Current() Info {
	return Info{
		Version:   version,
		Commit:    commit,
		BuildDate: buildDate,
	}
}
