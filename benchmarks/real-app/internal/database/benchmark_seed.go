package database

import (
	"context"
	"database/sql"
	"fmt"
)

func SeedBenchmarkProducts(ctx context.Context, db *sql.DB, count int) error {
	if count < 0 {
		return fmt.Errorf("product count must not be negative")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin benchmark product seed: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, "INSERT INTO products (name, category, price_cents) VALUES (?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare benchmark product seed: %w", err)
	}
	defer stmt.Close()

	categories := [...]string{"books", "electronics", "home", "outdoors"}
	for i := 1; i <= count; i++ {
		if _, err := stmt.ExecContext(ctx, fmt.Sprintf("Product %04d", i), categories[(i-1)%len(categories)], 1000+i); err != nil {
			return fmt.Errorf("seed benchmark product %d: %w", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit benchmark product seed: %w", err)
	}
	return nil
}
