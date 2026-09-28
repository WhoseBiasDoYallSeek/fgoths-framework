// Package health exposes liveness and readiness endpoints for the service.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type response struct {
	Status string `json:"status"`
}

// Live reports whether the HTTP process is running.
func Live(w http.ResponseWriter, _ *http.Request) {
	writeOK(w)
}

// ReadinessChecker is implemented by resources, such as a database pool, that
// need to be available before the service receives traffic.
type ReadinessChecker interface {
	PingContext(context.Context) error
}

// Ready reports whether the service can receive traffic. A nil checker is
// appropriate for stateless services.
func Ready(checker ReadinessChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if checker != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := checker.PingContext(ctx); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, response{Status: "not ready"})
				return
			}
		}
		writeOK(w)
	}
}

func writeOK(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, response{Status: "ok"})
}

func writeJSON(w http.ResponseWriter, status int, body response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
