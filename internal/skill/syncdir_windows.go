//go:build windows

package skill

import "os"

// Windows has no portable directory-fsync equivalent. The rooted rename is the
// publication boundary, so directory sync is intentionally a no-op here.
func syncRootDirectory(*os.Root, string) error { return nil }

func syncDirectory(string) error { return nil }
