// Package handlers provides HTTP request handlers for the application.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

var (
	counter int
	mu      sync.Mutex
)

// CounterGetHandler returns the current counter value
func CounterGetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mu.Lock()
	current := counter
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"counter": current})
}

// CounterIncrementHandler increments the counter (HTMX endpoint)
func CounterIncrementHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mu.Lock()
	counter++
	current := counter
	mu.Unlock()

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, "%d", current)
}

// CounterDecrementHandler decrements the counter (HTMX endpoint)
func CounterDecrementHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mu.Lock()
	counter--
	current := counter
	mu.Unlock()

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, "%d", current)
}

// CounterPutHandler sets the counter to a specific value
func CounterPutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Value int `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	mu.Lock()
	counter = req.Value
	current := counter
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"counter": current})
}

// CounterDeleteHandler resets the counter to zero
func CounterDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mu.Lock()
	counter = 0
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"counter": 0})
}
