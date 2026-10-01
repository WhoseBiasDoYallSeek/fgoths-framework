package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"real-app/models"
)

// ProductRepository persists product rows in SQLite.
type ProductRepository struct{ db *sql.DB }

func NewProductRepository(db *sql.DB) ProductRepository { return ProductRepository{db: db} }

func (r ProductRepository) Create(ctx context.Context, item models.Product) (models.Product, error) {
	res, err := r.db.ExecContext(ctx, "INSERT INTO products (name, category, price_cents) VALUES (?1, ?2, ?3)", item.Name, item.Category, item.PriceCents)
	if err != nil {
		return models.Product{}, fmt.Errorf("insert product: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return models.Product{}, fmt.Errorf("product id: %w", err)
	}
	item.ID = uint64(id)
	return item, nil
}

func (r ProductRepository) List(ctx context.Context) ([]models.Product, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id, name, category, price_cents, created_at FROM products ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	defer rows.Close()
	items := make([]models.Product, 0)
	for rows.Next() {
		var item models.Product
		var createdAt string
		if err := rows.Scan(&item.ID, &item.Name, &item.Category, &item.PriceCents, &createdAt); err != nil {
			return nil, fmt.Errorf("scan product: %w", err)
		}
		if t, err := time.Parse("2006-01-02 15:04:05", createdAt); err == nil {
			item.CreatedAt = t
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
