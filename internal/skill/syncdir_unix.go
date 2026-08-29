//go:build !windows

package skill

import (
	"errors"
	"os"
)

func syncRootDirectory(root *os.Root, name string) (resultErr error) {
	if name == "" {
		name = "."
	}
	directory, err := root.Open(name)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, directory.Close())
	}()
	return directory.Sync()
}

func syncDirectory(name string) (resultErr error) {
	directory, err := os.Open(name)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, directory.Close())
	}()
	return directory.Sync()
}
