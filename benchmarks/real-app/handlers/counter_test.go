package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCounterGetHandler(t *testing.T) {
	// Reset counter first
	req := httptest.NewRequest(http.MethodDelete, "/api/counter", nil)
	res := httptest.NewRecorder()
	CounterDeleteHandler(res, req)

	req = httptest.NewRequest(http.MethodGet, "/api/counter", nil)
	res = httptest.NewRecorder()

	CounterGetHandler(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	var resp map[string]int
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if resp["counter"] != 0 {
		t.Fatalf("expected counter 0, got %d", resp["counter"])
	}
}

func TestCounterGetHandlerRejectsNonGet(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/counter", nil)
	res := httptest.NewRecorder()

	CounterGetHandler(res, req)

	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", res.Code)
	}
}

func TestCounterPutHandler(t *testing.T) {
	// Reset first
	req := httptest.NewRequest(http.MethodDelete, "/api/counter", nil)
	res := httptest.NewRecorder()
	CounterDeleteHandler(res, req)

	// Set to specific value
	body := bytes.NewBufferString(`{"value": 42}`)
	req = httptest.NewRequest(http.MethodPut, "/api/counter", body)
	res = httptest.NewRecorder()

	CounterPutHandler(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	var resp map[string]int
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if resp["counter"] != 42 {
		t.Fatalf("expected counter 42, got %d", resp["counter"])
	}
}

func TestCounterPutHandlerRejectsNonPut(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/counter", nil)
	res := httptest.NewRecorder()

	CounterPutHandler(res, req)

	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", res.Code)
	}
}

func TestCounterPutHandlerRejectsInvalidBody(t *testing.T) {
	body := bytes.NewBufferString(`not json`)
	req := httptest.NewRequest(http.MethodPut, "/api/counter", body)
	res := httptest.NewRecorder()

	CounterPutHandler(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.Code)
	}
}

func TestCounterDeleteHandler(t *testing.T) {
	// Set a value first
	body := bytes.NewBufferString(`{"value": 99}`)
	req := httptest.NewRequest(http.MethodPut, "/api/counter", body)
	res := httptest.NewRecorder()
	CounterPutHandler(res, req)

	// Delete (reset)
	req = httptest.NewRequest(http.MethodDelete, "/api/counter", nil)
	res = httptest.NewRecorder()

	CounterDeleteHandler(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	var resp map[string]int
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if resp["counter"] != 0 {
		t.Fatalf("expected counter 0 after delete, got %d", resp["counter"])
	}
}

func TestCounterDeleteHandlerRejectsNonDelete(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/counter", nil)
	res := httptest.NewRecorder()

	CounterDeleteHandler(res, req)

	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", res.Code)
	}
}

func TestCounterIncrementHandler(t *testing.T) {
	// Reset first
	req := httptest.NewRequest(http.MethodDelete, "/api/counter", nil)
	res := httptest.NewRecorder()
	CounterDeleteHandler(res, req)

	req = httptest.NewRequest(http.MethodPost, "/api/counter/increment", nil)
	res = httptest.NewRecorder()

	CounterIncrementHandler(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
}

func TestCounterDecrementHandler(t *testing.T) {
	// Reset first
	req := httptest.NewRequest(http.MethodDelete, "/api/counter", nil)
	res := httptest.NewRecorder()
	CounterDeleteHandler(res, req)

	req = httptest.NewRequest(http.MethodPost, "/api/counter/decrement", nil)
	res = httptest.NewRecorder()

	CounterDecrementHandler(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
}
