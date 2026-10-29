// Package dbmigrate runs the embedded goose migrations.
package dbmigrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"

	"discipline-backend/migrations"
)

func provider(db *sql.DB) (*goose.Provider, error) {
	return goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
}

// Up applies every pending migration.
func Up(ctx context.Context, db *sql.DB) error {
	p, err := provider(db)
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}

// UpTo applies pending migrations up to and including version.
func UpTo(ctx context.Context, db *sql.DB, version int64) error {
	p, err := provider(db)
	if err != nil {
		return err
	}
	_, err = p.UpTo(ctx, version)
	return err
}

// DownTo rolls back migrations until the database is at version.
func DownTo(ctx context.Context, db *sql.DB, version int64) error {
	p, err := provider(db)
	if err != nil {
		return err
	}
	_, err = p.DownTo(ctx, version)
	return err
}

// Version returns the database's current migration version (0 if none applied).
func Version(ctx context.Context, db *sql.DB) (int64, error) {
	p, err := provider(db)
	if err != nil {
		return 0, err
	}
	return p.GetDBVersion(ctx)
}

// Latest returns the highest embedded migration version.
func Latest() (int64, error) {
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return 0, err
	}
	var latest int64
	for _, name := range names {
		v, err := goose.NumericComponent(name)
		if err != nil {
			return 0, fmt.Errorf("migration %s: %w", name, err)
		}
		latest = max(latest, v)
	}
	return latest, nil
}

// Status returns one line per migration with its applied state.
func Status(ctx context.Context, db *sql.DB) ([]string, error) {
	p, err := provider(db)
	if err != nil {
		return nil, err
	}
	statuses, err := p.Status(ctx)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(statuses))
	for _, s := range statuses {
		applied := "pending"
		if s.State == goose.StateApplied {
			applied = "applied " + s.AppliedAt.Format("2006-01-02 15:04:05")
		}
		lines = append(lines, fmt.Sprintf("%05d  %-45s %s", s.Source.Version, s.Source.Path, applied))
	}
	return lines, nil
}

// RequireLatest fails unless every embedded migration has been applied, so the
// server never runs against a schema it was not written for.
func RequireLatest(ctx context.Context, db *sql.DB) error {
	latest, err := Latest()
	if err != nil {
		return err
	}
	current, err := Version(ctx, db)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current != latest {
		return fmt.Errorf("database schema is at version %d but this build needs %d; run `migrate up` first", current, latest)
	}
	return nil
}
