package buildinfo

import "testing"

func TestCurrentReturnsDefaults(t *testing.T) {
	info := Current()

	if info.Version == "" {
		t.Fatal("Version is empty")
	}
	if info.Commit == "" {
		t.Fatal("Commit is empty")
	}
	if info.BuildDate == "" {
		t.Fatal("BuildDate is empty")
	}
}
