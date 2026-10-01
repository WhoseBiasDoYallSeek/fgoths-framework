package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"real-app/internal/database"
	"real-app/models"
	"real-app/pkg/runtime"
)

func writeProductJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// registerProductRoutes mounts the JSON API for products.
func registerProductRoutes(registrar runtime.Registrar, db *sql.DB) {
	repo := database.NewProductRepository(db)
	registrar.Handle(http.MethodGet, "/api/products", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := repo.List(r.Context())
		if err != nil {
			writeProductJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeProductJSON(w, http.StatusOK, items)
	}))
	registrar.Handle(http.MethodPost, "/api/products", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var item models.Product
		if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
			writeProductJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		created, err := repo.Create(r.Context(), item)
		if err != nil {
			writeProductJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeProductJSON(w, http.StatusCreated, created)
	}))
}
