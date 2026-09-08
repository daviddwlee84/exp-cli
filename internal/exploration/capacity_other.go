//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package exploration

// Platforms without statfs still check creation and report write failures.
func CheckCapacity(string) error { return nil }
