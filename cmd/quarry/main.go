package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/AryanHamedani/quarry/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cmd := cli.NewRootCommand()
	cmd.SetContext(ctx)
	if err := cmd.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
