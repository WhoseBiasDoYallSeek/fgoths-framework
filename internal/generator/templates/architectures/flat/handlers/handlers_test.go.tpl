package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)

	Status("demo")(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "up" {
		t.Errorf("status = %v, want up", body["status"])
	}
	if body["service"] != "demo" {
		t.Errorf("service = %v, want demo", body["service"])
	}
}
