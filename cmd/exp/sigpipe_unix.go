//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package main

import (
	"os/signal"
	"syscall"
)

// ignoreBrokenPipeSignal lets stdout writes report EPIPE to the CLI boundary.
// A downstream consumer closing a successful result stream must not terminate
// the process after a mutation has already committed.
func ignoreBrokenPipeSignal() {
	signal.Ignore(syscall.SIGPIPE)
}
