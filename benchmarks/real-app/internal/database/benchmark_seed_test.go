package database

import (
	"context"
	"database/sql"
	"testing"
)

func TestSeedBenchmarkProducts(t *testing.T) {
	db, err := sql.Open("sqlite", "file:benchmark-seed-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE products (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		category TEXT NOT NULL,
		price_cents INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}

	if err := SeedBenchmarkProducts(context.Background(), db, 6); err != nil {
		t.Fatalf("SeedBenchmarkProducts() error = %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM products").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 6 {
		t.Fatalf("product count = %d, want 6", count)
	}

	var name, category string
	var price int
	if err := db.QueryRow("SELECT name, category, price_cents FROM products ORDER BY id LIMIT 1").Scan(&name, &category, &price); err != nil {
		t.Fatal(err)
	}
	if name != "Product 0001" || category != "books" || price != 1001 {
		t.Fatalf("first product = (%q, %q, %d)", name, category, price)
	}
}

func TestSeedBenchmarkProductsRejectsNegativeCount(t *testing.T) {
	if err := SeedBenchmarkProducts(context.Background(), nil, -1); err == nil {
		t.Fatal("expected negative count to be rejected")
	}
}
