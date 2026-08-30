//go:build windows

package execx

import "os/exec"

// configureProcessCancellation retains exec.CommandContext's direct-child kill
// on Windows. Descendant termination requires Job Object integration, which is
// not implemented or claimed by this milestone.
func configureProcessCancellation(_ *exec.Cmd) {}

func cleanupProcessGroup(_ *exec.Cmd) error { return nil }
