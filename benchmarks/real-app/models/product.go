package models

import "time"

// Product is a persisted entity managed by the products CRUD slice.
type Product struct {
	ID uint64 `json:"id"`
	Name string `json:"name"`
	Category string `json:"category"`
	PriceCents int64 `json:"price_cents"`
	CreatedAt time.Time `json:"created_at"`
}
