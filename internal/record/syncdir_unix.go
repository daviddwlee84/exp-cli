//go:build unix

package record

import (
	"os"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
)

func syncPublishedFile(*os.Root, string) error { return nil }

func syncDirectory(root *os.Root) error {
	return pathx.SyncRoot(root)
}
