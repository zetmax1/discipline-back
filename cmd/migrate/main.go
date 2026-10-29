// Command migrate applies the embedded schema migrations.
//
//	migrate [-database URL] up | up-to N | down | down-to N | status | version
//
// The connection comes from -database, else $DATABASE_URL, else the DB_* variables
// used by the server (DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME, DB_SSLMODE).
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"

	"discipline-backend/internal/dbmigrate"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	database := fs.String("database", "", "Postgres URL (default: $DATABASE_URL, then DB_* variables)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: migrate [-database URL] up | up-to N | down | down-to N | status | version")
	}

	dsn, err := resolveDSN(*database)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()

	switch cmd := fs.Arg(0); cmd {
	case "up":
		return report(ctx, db, dbmigrate.Up(ctx, db))
	case "up-to", "down-to":
		v, err := strconv.ParseInt(fs.Arg(1), 10, 64)
		if err != nil {
			return fmt.Errorf("%s needs a version number", cmd)
		}
		if cmd == "up-to" {
			return report(ctx, db, dbmigrate.UpTo(ctx, db, v))
		}
		return report(ctx, db, dbmigrate.DownTo(ctx, db, v))
	case "down":
		current, err := dbmigrate.Version(ctx, db)
		if err != nil {
			return err
		}
		if current == 0 {
			return fmt.Errorf("nothing to roll back")
		}
		return report(ctx, db, dbmigrate.DownTo(ctx, db, current-1))
	case "status":
		lines, err := dbmigrate.Status(ctx, db)
		for _, l := range lines {
			fmt.Println(l)
		}
		return err
	case "version":
		return report(ctx, db, nil)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func report(ctx context.Context, db *sql.DB, err error) error {
	if err != nil {
		return err
	}
	v, err := dbmigrate.Version(ctx, db)
	if err != nil {
		return err
	}
	latest, _ := dbmigrate.Latest()
	fmt.Printf("schema version %d (latest %d)\n", v, latest)
	return nil
}

func resolveDSN(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	_ = godotenv.Load()
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v, nil
	}
	if os.Getenv("DB_PASSWORD") == "" {
		return "", fmt.Errorf("no database: pass -database, set DATABASE_URL, or set DB_* variables")
	}
	sslmode := os.Getenv("DB_SSLMODE")
	if sslmode == "" {
		sslmode = "require"
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(getenv("DB_USER", "postgres"), os.Getenv("DB_PASSWORD")),
		Host:     getenv("DB_HOST", "localhost") + ":" + getenv("DB_PORT", "5432"),
		Path:     "/" + getenv("DB_NAME", "discipline"),
		RawQuery: url.Values{"sslmode": {sslmode}}.Encode(),
	}
	return u.String(), nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
