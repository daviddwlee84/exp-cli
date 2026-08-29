// Command exp is a Git-native research control plane.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/daviddwlee84/exp-cli/internal/cli"
)

func main() {
	os.Exit(run())
}

// run keeps cleanup inside a returning function, so its defers complete before
// main passes the resulting status to os.Exit.
func run() int {
	ignoreBrokenPipeSignal()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return cli.Execute(ctx, os.Stdin, os.Stdout, os.Stderr, os.Args[1:])
}
