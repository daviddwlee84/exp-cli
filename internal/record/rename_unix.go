//go:build unix && !darwin && !linux

package record

import "os"

func exchangeAtomic(*os.File, string, string) error {
	return ErrAtomicCASUnsupported
}
