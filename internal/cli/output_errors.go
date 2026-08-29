package cli

import (
	"errors"
	"io"
)

// successfulOutputError treats a downstream reader closing a successful result
// stream as delivery abandonment, not a command failure. In particular, a
// committed mutation must not become retryable merely because its consumer
// stopped reading. Other writer failures remain command errors.
func successfulOutputError(err error) error {
	if outputWasAbandoned(err) {
		return nil
	}
	return safeCLIError(err)
}

func outputWasAbandoned(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !outputWasAbandoned(cause) {
				return false
			}
		}
		return true
	}
	return errors.Is(err, io.ErrClosedPipe) || isBrokenPipeError(err)
}
