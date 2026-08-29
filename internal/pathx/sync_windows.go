//go:build windows

package pathx

import "os"

// Windows does not provide a portable directory fsync for os.Root handles.
// Rooted rename/link operations use the platform's synchronous metadata APIs.
func SyncRoot(*os.Root) error { return nil }

// os.Root rejects escapes through reparse points. The generic caller brackets
// this open with lstat/handle/lstat identity checks because Windows does not
// expose O_NOFOLLOW through os.OpenFile flags.
func openFileNoFollow(root *os.Root, relative string) (*os.File, error) {
	return root.OpenFile(relative, os.O_RDONLY, 0)
}
