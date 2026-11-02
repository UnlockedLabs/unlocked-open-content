// Package worker is the product's background worker.
package worker

import (
	"context"
	"log/slog"
)

// Run runs the worker until ctx is canceled. A product starts its own jobs
// here, before waiting on ctx.
func Run(ctx context.Context) error {
	slog.Info("worker started")
	<-ctx.Done()
	slog.Info("worker stopped")
	return nil
}
