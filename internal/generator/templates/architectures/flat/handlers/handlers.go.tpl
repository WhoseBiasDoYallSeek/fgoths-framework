// Package handlers holds the HTTP handlers of the application. Add your
// endpoints here; keep business rules in small functions beside their
// handler or in a separate package when the project grows.
package handlers

import (
	"encoding/json"
	"net/http"
)

// Status returns a handler that reports the service name and status. Use it
// as a template for your own endpoints.
func Status(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"service": service,
			"status":  "up",
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
