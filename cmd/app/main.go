// Command app is Open Content's one binary: "app serve" runs the API, "app
// worker" seeds a new database and runs the jobs and their schedules, and
// "app migrate" migrates the database. For a local database, "app seed-dev" adds sample data
// and "app migrate-reset" rolls back the migrations. serve and worker stop
// cleanly on SIGINT or SIGTERM.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-open-content/internal/database"
	"github.com/UnlockedLabs/unlocked-open-content/internal/opencontent"
	"github.com/UnlockedLabs/unlocked-open-content/internal/server"
	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const usage = "usage: app serve | app worker | app migrate | app seed-dev | app migrate-reset"

func main() {
	initLogging()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		log.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "serve":
		return serve(ctx)
	case "worker":
		return work(ctx)
	case "migrate":
		return migrate(ctx)
	case "seed-dev":
		return seedDev(ctx)
	case "migrate-reset":
		return migrateReset(ctx)
	default:
		return fmt.Errorf("unknown command %q: %s", args[0], usage)
	}
}

// serve runs the API once the database is migrated. KRATOS_PUBLIC_URL says
// whether a login session is active, and core, at APP_URL, who its user is.
// Core asks its questions with OPEN_CONTENT_TOKEN, the site's secret.
func serve(ctx context.Context) error {
	token := os.Getenv("OPEN_CONTENT_TOKEN")
	if token == "" {
		return errors.New("OPEN_CONTENT_TOKEN must be set: it's the secret core asks Open Content's questions with")
	}
	db, nc, err := connect(ctx)
	if err != nil {
		return err
	}
	defer closeAll(db, nc)
	sessions := identity.NewSessionChecker(os.Getenv("KRATOS_PUBLIC_URL"), os.Getenv("APP_URL"), nil)
	app, err := opencontent.NewHandler(ctx, db, nc, sessions.Require, token)
	if err != nil {
		return err
	}
	return server.Serve(ctx, envOr("ADDR", ":8080"), os.Getenv("PATH_PREFIX"), server.VersionFromEnv(), app)
}

// work runs the worker once the database is migrated.
func work(ctx context.Context) error {
	db, nc, err := connect(ctx)
	if err != nil {
		return err
	}
	defer closeAll(db, nc)
	return opencontent.Work(ctx, db, nc)
}

// migrate runs Open Content's migrations as the database's own user. The
// worker seeds a new database.
func migrate(ctx context.Context) error {
	owner, err := database.Open(ctx, database.DSN("prefer"))
	if err != nil {
		return err
	}
	defer func() { _ = owner.Close() }()
	if err := opencontent.Migrate(ctx, owner); err != nil {
		return err
	}
	log.Info("Migrations completed successfully")
	return nil
}

// seedDev adds sample data for core's dev residents. It runs only with
// APP_ENV=dev, so it can never add sample data to a real site.
func seedDev(ctx context.Context) error {
	if os.Getenv("APP_ENV") != "dev" {
		return errors.New("seed-dev adds sample data, so it runs only with APP_ENV=dev")
	}
	db, err := connectDB(ctx)
	if err != nil {
		return err
	}
	defer closeAll(db, nil)
	return opencontent.SeedDev(ctx, db)
}

// migrateReset rolls back Open Content's migrations, for core's
// `make migrate-fresh`. It runs only with APP_ENV=dev, so it can never touch a
// real site.
func migrateReset(ctx context.Context) error {
	if os.Getenv("APP_ENV") != "dev" {
		return errors.New("migrate-reset rolls back the migrations, so it runs only with APP_ENV=dev")
	}
	owner, err := database.Open(ctx, database.DSN("prefer"))
	if err != nil {
		return err
	}
	defer func() { _ = owner.Close() }()
	return opencontent.Reset(ctx, owner)
}

// connect opens Open Content's database connection (connectDB) and connects
// to NATS with NATS_URL, NATS_USER and NATS_PASSWORD.
func connect(ctx context.Context) (*gorm.DB, *nats.Conn, error) {
	db, err := connectDB(ctx)
	if err != nil {
		return nil, nil, err
	}
	nc, err := nats.Connect(os.Getenv("NATS_URL"), nats.UserInfo(os.Getenv("NATS_USER"), os.Getenv("NATS_PASSWORD")))
	if err != nil {
		closeAll(db, nil)
		return nil, nil, fmt.Errorf("connect to NATS: %w", err)
	}
	return db, nc, nil
}

// connectDB waits until the database is migrated, then opens Open Content's
// connection to it.
func connectDB(ctx context.Context) (*gorm.DB, error) {
	owner, err := database.Open(ctx, database.DSN("allow"))
	if err != nil {
		return nil, err
	}
	err = opencontent.WaitForMigrations(ctx, owner)
	_ = owner.Close()
	if err != nil {
		return nil, fmt.Errorf("wait for migrations: %w", err)
	}
	return database.OpenAs(ctx, database.DSN("allow"), opencontent.Schema)
}

// closeAll closes db, then nc, which may be nil.
func closeAll(db *gorm.DB, nc *nats.Conn) {
	if sqlDB, err := db.DB(); err == nil {
		if err := sqlDB.Close(); err != nil {
			log.Errorf("close database: %v", err)
		}
	}
	if nc != nil {
		nc.Close()
	}
}

// initLogging logs JSON at LOG_LEVEL (info by default), as core does.
func initLogging() {
	log.SetFormatter(&log.JSONFormatter{})
	level, err := log.ParseLevel(envOr("LOG_LEVEL", "info"))
	if err != nil {
		log.Errorf("LOG_LEVEL: %v; logging at info", err)
		level = log.InfoLevel
	}
	log.SetLevel(level)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
