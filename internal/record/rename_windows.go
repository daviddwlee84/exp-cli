//go:build windows

package record

import "os"

func exchangeAtomic(*os.File, string, string) error {
	return ErrAtomicCASUnsupported
}
