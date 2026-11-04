package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"
	log "github.com/sirupsen/logrus"
)

// Schema is the Postgres schema that holds Open Content's tables, and the
// role its connection runs as.
const Schema = "open_content"

// ledgerTable is the goose version table for Open Content's migrations. It is
// the one core used while Open Content was its module, so a site that already
// ran them doesn't run them again.
const ledgerTable = "public.goose_db_version_opencontent"

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate runs Open Content's migrations on db, which connects as the
// database's own user, not as the open_content role. Core's migrations must
// have run first: the first one moves tables core's migrations created.
func Migrate(ctx context.Context, db *sql.DB) error {
	p, err := migrationProvider(db)
	if err != nil {
		return err
	}
	results, err := p.Up(ctx)
	for _, r := range results {
		log.Infof("Applied migration %s in %s", r.Source.Path, r.Duration)
	}
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// Reset rolls back every Open Content migration on db, which connects as the
// database's own user, so core's reset can then drop the tables. Run it before
// core's: the first migration's rollback moves the tables back to public.
func Reset(ctx context.Context, db *sql.DB) error {
	p, err := migrationProvider(db)
	if err != nil {
		return err
	}
	results, err := p.DownTo(ctx, 0)
	for _, r := range results {
		log.Infof("Rolled back migration %s in %s", r.Source.Path, r.Duration)
	}
	if err != nil {
		return fmt.Errorf("roll back migrations: %w", err)
	}
	return nil
}

// migrationProvider is goose with Open Content's migrations and ledger.
func migrationProvider(db *sql.DB) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys,
		goose.WithTableName(ledgerTable),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	return p, nil
}

// migrationPollInterval is how often WaitForMigrations checks, as core's
// does.
const migrationPollInterval = 5 * time.Second

// WaitForMigrations waits until db has every migration this image ships, so
// serve and worker never run against an older schema. It reads the ledger
// only, and waits out errors, since the ledger doesn't exist until migrate
// first runs. It returns ctx's error if ctx is canceled first.
func WaitForMigrations(ctx context.Context, db *sql.DB) error {
	p, err := migrationProvider(db)
	if err != nil {
		return err
	}
	sources := p.ListSources()
	latest := sources[len(sources)-1].Version
	for {
		var applied int64
		err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_id), 0) FROM `+ledgerTable+` WHERE is_applied`).Scan(&applied)
		switch {
		case err != nil:
			log.Warnf("Waiting for migrations: %v", err)
		case applied < latest:
			log.Infof("Waiting for migrations: at %d of %d", applied, latest)
		default:
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(migrationPollInterval):
		}
	}
}
