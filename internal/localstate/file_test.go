package localstate

import (
	"path/filepath"
	"testing"
)

func TestXDGHomeResolutionUsesOnlyAbsoluteOverrides(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	state := filepath.Join(home, "state")
	config := filepath.Join(home, "config")
	cache := filepath.Join(home, "cache")
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", cache)
	if got, err := StateHome(); err != nil || got != state {
		t.Fatalf("StateHome = %q, %v; want %q", got, err, state)
	}
	if got, err := ConfigHome(); err != nil || got != config {
		t.Fatalf("ConfigHome = %q, %v; want %q", got, err, config)
	}
	if got, err := CacheHome(); err != nil || got != cache {
		t.Fatalf("CacheHome = %q, %v; want %q", got, err, cache)
	}

	t.Setenv("XDG_STATE_HOME", "relative/state")
	t.Setenv("XDG_CONFIG_HOME", "relative/config")
	t.Setenv("XDG_CACHE_HOME", "relative/cache")
	if got, err := StateHome(); err != nil || got != filepath.Join(home, ".local", "state") {
		t.Fatalf("relative XDG_STATE_HOME = %q, %v", got, err)
	}
	if got, err := ConfigHome(); err != nil || got != filepath.Join(home, ".config") {
		t.Fatalf("relative XDG_CONFIG_HOME = %q, %v", got, err)
	}
	if got, err := CacheHome(); err != nil || got != filepath.Join(home, ".cache") {
		t.Fatalf("relative XDG_CACHE_HOME = %q, %v", got, err)
	}
}
