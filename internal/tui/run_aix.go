//go:build aix

package tui

import (
	"context"
	"io"
)

// Available reports that Bubble Tea terminal control is intentionally disabled
// on AIX while the platform-neutral snapshot/model code remains buildable.
func Available() bool { return false }

func Run(context.Context, io.Reader, io.Writer, Options) error { return ErrUnavailable }
