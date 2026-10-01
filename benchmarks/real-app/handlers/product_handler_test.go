package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"real-app/pkg/runtime"

	_ "modernc.org/sqlite"
)

func openProductHandlerTestDB(t *testing.T) *sql.DB {
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

func TestRegisterProductRoutesCreateAndList(t *testing.T) {
	db := openProductHandlerTestDB(t)
	server := runtime.NewServer("")
	registerProductRoutes(server, db)

	body, err := json.Marshal(map[string]any{
		"name": "test",
		"category": "test",
		"price_cents": 1,
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	postReq := httptest.NewRequest(http.MethodPost, "/api/products", bytes.NewReader(body))
	postRes := httptest.NewRecorder()
	server.Handler.ServeHTTP(postRes, postReq)
	if postRes.Code != http.StatusCreated {
		t.Fatalf("POST /api/products = %d, want %d; body=%s", postRes.Code, http.StatusCreated, postRes.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/products", nil)
	getRes := httptest.NewRecorder()
	server.Handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("GET /api/products = %d, want %d; body=%s", getRes.Code, http.StatusOK, getRes.Body.String())
	}

	var items []map[string]any
	if err := json.Unmarshal(getRes.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 product, got %d", len(items))
	}
}
