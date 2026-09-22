// Package database configures the MySQL connection used by the service.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"{{.ProjectName}}/migrations"

	_ "github.com/go-sql-driver/mysql"
)

const defaultDSN = "root@tcp(localhost:3306)/{{.ProjectName}}?parseTime=true&multiStatements=true"

// Open returns a verified MySQL connection pool. DATABASE_URL may override
// the default local connection string.
func Open() (*sql.DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = defaultDSN
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open MySQL: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping MySQL: %w", err)
	}
	if err := migrations.Apply(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}

	return db, nil
}
