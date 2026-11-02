// Command app is the product's one binary: "app serve" runs the API and
// "app worker" runs the background worker. Both stop cleanly on SIGINT or
// SIGTERM.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/UnlockedLabs/unlocked-product-template/internal/server"
	"github.com/UnlockedLabs/unlocked-product-template/internal/worker"
)

const usage = "usage: app serve | app worker"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "serve":
		return server.Serve(ctx, envOr("ADDR", ":8080"), os.Getenv("PATH_PREFIX"), server.VersionFromEnv())
	case "worker":
		return worker.Run(ctx)
	default:
		return fmt.Errorf("unknown command %q: %s", args[0], usage)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
