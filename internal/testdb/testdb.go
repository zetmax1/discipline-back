// Package testdb gives integration tests their own throwaway Postgres database.
//
// Tests are skipped unless TEST_DATABASE_URL points at a disposable server, e.g.
// the one started by docker-compose.test.yml. Each call creates a fresh database
// and drops it when the test finishes.
package testdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"discipline-backend/internal/dbmigrate"
)

// EnvVar names the admin connection URL of the throwaway test server.
const EnvVar = "TEST_DATABASE_URL"

// NewEmpty returns a connection to a brand-new, empty database.
func NewEmpty(t *testing.T) *sql.DB {
	t.Helper()
	dsn := newDatabase(t)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// NewMigrated returns a fresh database with every migration applied.
func NewMigrated(t *testing.T) (*gorm.DB, *sql.DB) {
	t.Helper()
	sqlDB := NewEmpty(t)
	if err := dbmigrate.Up(context.Background(), sqlDB); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}
	return gdb, sqlDB
}

func newDatabase(t *testing.T) string {
	t.Helper()
	adminURL := os.Getenv(EnvVar)
	if adminURL == "" {
		t.Skipf("%s not set; start docker-compose.test.yml to run integration tests", EnvVar)
	}
	admin, err := sql.Open("pgx", adminURL)
	if err != nil {
		t.Fatalf("open admin db: %v", err)
	}
	defer admin.Close()

	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	name := "t_" + hex.EncodeToString(buf)
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		admin, err := sql.Open("pgx", adminURL)
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
	})

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvVar, err)
	}
	u.Path = "/" + name
	return u.String()
}
