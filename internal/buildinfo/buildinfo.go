// Package buildinfo exposes immutable build metadata injected at link time.
package buildinfo

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

// Current returns the build metadata embedded in the running binary.
func Current() Info {
	return Info{
		Version:   version,
		Commit:    commit,
		BuildDate: buildDate,
	}
}
