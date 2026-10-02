package httpserver

import "testing"

func TestServiceInfoValidate(t *testing.T) {
	t.Parallel()

	valid := ServiceInfo{
		Service:   "サービス",
		Version:   "v1.2.3",
		Commit:    "abc123",
		BuildTime: "2026-10-03T00:00:00Z",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() valid metadata error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ServiceInfo)
	}{
		{
			name: "empty service",
			mutate: func(info *ServiceInfo) {
				info.Service = " "
			},
		},
		{
			name: "invalid UTF-8 service",
			mutate: func(info *ServiceInfo) {
				info.Service = string([]byte{0xff})
			},
		},
		{
			name: "empty version",
			mutate: func(info *ServiceInfo) {
				info.Version = ""
			},
		},
		{
			name: "invalid UTF-8 version",
			mutate: func(info *ServiceInfo) {
				info.Version = "v1-" + string([]byte{0xc3, 0x28})
			},
		},
		{
			name: "empty commit",
			mutate: func(info *ServiceInfo) {
				info.Commit = "\t"
			},
		},
		{
			name: "invalid UTF-8 commit",
			mutate: func(info *ServiceInfo) {
				info.Commit = string([]byte{0xff})
			},
		},
		{
			name: "empty build time",
			mutate: func(info *ServiceInfo) {
				info.BuildTime = "\n"
			},
		},
		{
			name: "invalid UTF-8 build time",
			mutate: func(info *ServiceInfo) {
				info.BuildTime = string([]byte{0xff})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			info := valid
			tt.mutate(&info)
			if err := info.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want invalid metadata error")
			}
		})
	}
}
