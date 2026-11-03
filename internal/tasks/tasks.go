// Package tasks is Open Content's worker (`app worker`): the jobs it runs,
// and the schedule it publishes the scheduled ones on.
package tasks

import (
	"context"
	"fmt"
	"os"

	"github.com/UnlockedLabs/unlocked-open-content/internal/database"
	"github.com/go-co-op/gocron/v2"
	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
)

// Work runs the worker until ctx is canceled. It first seeds a new database
// (WaitAndSeed), so the scheduled jobs find the Kiwix and YouTube providers,
// then runs Open Content's jobs as they're published, and publishes the
// scheduled ones on their schedules. Canceling ctx also stops the jobs that
// are running.
func Work(ctx context.Context, db *database.DB, nc *nats.Conn) error {
	if err := db.WaitAndSeed(ctx); err != nil {
		return err
	}
	w := &worker{db: db}
	if err := w.subscribe(ctx, nc); err != nil {
		return err
	}
	s, err := gocron.NewScheduler()
	if err != nil {
		return fmt.Errorf("create scheduler: %w", err)
	}
	p := &jobPublisher{db: db, publish: nc.PublishMsg}
	scheduleJobs(cronScheduler{s}, p, os.Getenv("APP_ENV") == "dev")
	s.Start()
	log.Info("Worker started")

	<-ctx.Done()
	if err := s.Shutdown(); err != nil {
		return fmt.Errorf("stop scheduler: %w", err)
	}
	log.Info("Worker stopped")
	return nil
}
