package buildinfo

import "testing"

func TestCurrentReturnsDefaults(t *testing.T) {
	info := Current()

	if err := info.Validate(); err != nil {
		t.Fatalf("default build info validation error = %v", err)
	}
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


func TestInfoValidateRejectsInvalidIdentityText(t *testing.T) {
	t.Parallel()

	base := Info{
		Version:   "v1.2.3",
		Commit:    "abcdef123456",
		BuildDate: "2026-10-02T00:00:00Z",
	}
	tests := []struct {
		name   string
		mutate func(*Info)
	}{
		{
			name: "empty version",
			mutate: func(info *Info) {
				info.Version = " "
			},
		},
		{
			name: "invalid UTF-8 version",
			mutate: func(info *Info) {
				info.Version = "v1-" + string([]byte{0xff})
			},
		},
		{
			name: "empty commit",
			mutate: func(info *Info) {
				info.Commit = "\t"
			},
		},
		{
			name: "invalid UTF-8 commit",
			mutate: func(info *Info) {
				info.Commit = string([]byte{0xc3, 0x28})
			},
		},
		{
			name: "empty build date",
			mutate: func(info *Info) {
				info.BuildDate = "\n"
			},
		},
		{
			name: "invalid UTF-8 build date",
			mutate: func(info *Info) {
				info.BuildDate = string([]byte{0xff})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			info := base
			tt.mutate(&info)
			if err := info.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want malformed build metadata error")
			}
		})
	}
}
