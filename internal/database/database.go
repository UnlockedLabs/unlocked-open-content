// Package database is Open Content's database: its connections to core's
// Postgres database, which Open Content shares, with the same settings core
// uses, its migrations and seed, and every query.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB is Open Content's database connection, with its queries. The queries
// moved from core's database package unchanged (EN-192). A method that takes
// ctx records the caller as the create or update user; one without writes no
// user.
type DB struct{ *gorm.DB }

func NewDB(db *gorm.DB) *DB {
	return &DB{db}
}

// DSN is the database's connection string: APP_DSN, else one built from
// DB_HOST, DB_PORT, DB_USER, DB_PASSWORD and DB_NAME, as core builds it.
func DSN(sslMode string) string {
	if dsn := os.Getenv("APP_DSN"); dsn != "" {
		return dsn
	}
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		os.Getenv("DB_HOST"), os.Getenv("DB_PORT"), os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_NAME"), sslMode)
}

// Open opens a connection as the database's own user, for migrations.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return db, nil
}

// OpenAs opens a pool whose sessions switch to role and look up tables in the
// schema of the same name first, then public, as core's module connections
// did. The database's own user must be a member of role.
func OpenAs(ctx context.Context, dsn, role string) (*gorm.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("read database connection string: %w", err)
	}
	cfg.RuntimeParams["search_path"] = role + ", public"
	setRole := stdlib.OptionAfterConnect(func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		return err
	})
	sqlDB := stdlib.OpenDB(*cfg, setRole)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("connect to database as %s: %w", role, err)
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		// Errors and slow queries only, as core logs them.
		Logger: logger.New(logrus.StandardLogger(), logger.Config{
			LogLevel:                  logger.Warn,
			SlowThreshold:             200 * time.Millisecond,
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      true,
		}),
	})
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open gorm: %w", err)
	}
	return db, nil
}
