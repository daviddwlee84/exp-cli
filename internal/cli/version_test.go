package cli

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersionPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		linked string
		info   *debug.BuildInfo
		want   string
	}{
		{
			name:   "ldflags win",
			linked: "v1.2.3",
			info:   &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}},
			want:   "v1.2.3",
		},
		{
			name:   "go install module version",
			linked: developmentVersion,
			info:   &debug.BuildInfo{Main: debug.Module{Version: "v2.0.1"}},
			want:   "v2.0.1",
		},
		{
			name:   "local vcs revision",
			linked: developmentVersion,
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "0123456789abcdef"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			want: "dev+0123456789ab",
		},
		{
			name:   "dirty local vcs revision",
			linked: "",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "fedcba9876543210"},
				{Key: "vcs.modified", Value: "true"},
			}},
			want: "dev+fedcba987654.dirty",
		},
		{
			name:   "development fallback",
			linked: "(devel)",
			info:   nil,
			want:   developmentVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.linked, tt.info); got != tt.want {
				t.Fatalf("resolveVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRootUsesLinkedVersion(t *testing.T) {
	original := Version
	Version = "v3.4.5"
	t.Cleanup(func() { Version = original })

	root := NewRootCommandWithIO(t.Context(), nil, nil, nil)
	if got, want := root.Version, "v3.4.5"; got != want {
		t.Fatalf("root.Version = %q, want %q", got, want)
	}
}
