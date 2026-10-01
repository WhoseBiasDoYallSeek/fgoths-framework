package database

import (
	"context"
	"database/sql"
	"testing"

	"real-app/models"

	_ "modernc.org/sqlite"
)

func openProductTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE products (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		category TEXT NOT NULL,
		price_cents INTEGER NOT NULL,
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create products table: %v", err)
	}
	return db
}

func TestNewProductRepositoryCreateAndList(t *testing.T) {
	db := openProductTestDB(t)
	repo := NewProductRepository(db)
	ctx := context.Background()

	item := models.Product{
		Name: "test",
		Category: "test",
		PriceCents: 1,
	}
	created, err := repo.Create(ctx, item)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected Create to assign a non-zero ID")
	}

	items, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 product, got %d", len(items))
	}
	if items[0].ID != created.ID {
		t.Fatalf("expected listed product ID %d, got %d", created.ID, items[0].ID)
	}
}
