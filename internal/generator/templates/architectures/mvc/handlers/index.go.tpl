// Package handlers provides HTTP request handlers for the application.
// It includes page handlers for rendering server-side HTML and API endpoints for HTMX interactions.
package handlers

import (
	"net/http"

	"{{.ProjectName}}/views"
)

// IndexHandler serves the home page.
func IndexHandler(w http.ResponseWriter, r *http.Request) {
	if err := views.Index("{{.ProjectName}}").Render(r.Context(), w); err != nil {
		http.Error(w, "failed to render home page", http.StatusInternalServerError)
	}
}

// AboutHandler serves the about page.
func AboutHandler(w http.ResponseWriter, r *http.Request) {
	if err := views.About("{{.ProjectName}}").Render(r.Context(), w); err != nil {
		http.Error(w, "failed to render about page", http.StatusInternalServerError)
	}
}
