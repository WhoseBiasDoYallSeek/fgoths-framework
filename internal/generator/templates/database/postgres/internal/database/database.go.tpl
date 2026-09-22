// Package database configures the PostgreSQL connection used by the service.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"{{.ProjectName}}/migrations"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultDSN = "postgres://localhost:5432/{{.ProjectName}}?sslmode=disable"

// Open returns a verified PostgreSQL connection pool. DATABASE_URL may
// override the default local connection string.
func Open() (*sql.DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = defaultDSN
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	if err := migrations.Apply(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}

	return db, nil
}
