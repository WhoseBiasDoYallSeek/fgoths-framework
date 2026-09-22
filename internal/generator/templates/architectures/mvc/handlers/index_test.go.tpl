package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIndexHandlerRendersHomePage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()

	IndexHandler(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, "{{.ProjectName}}") {
		t.Fatalf("expected the home page to mention the project name, got %q", body)
	}
	if !strings.Contains(body, "Welcome to") {
		t.Fatalf("expected the home page to render the welcome heading, got %q", body)
	}
}

func TestAboutHandlerRendersAboutPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/about", nil)
	res := httptest.NewRecorder()

	AboutHandler(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, "FGOTHS") {
		t.Fatalf("expected the about page to mention FGOTHS, got %q", body)
	}
	if !strings.Contains(body, "Why FGOTHS?") {
		t.Fatalf("expected the about page to render the mission section, got %q", body)
	}
}
