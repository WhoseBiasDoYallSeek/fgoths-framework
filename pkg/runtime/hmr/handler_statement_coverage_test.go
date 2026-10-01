// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
package hmr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type cancelAfterKeepaliveWriter struct {
	*httptest.ResponseRecorder
	cancel  context.CancelFunc
	flushes int
}

func (w *cancelAfterKeepaliveWriter) Flush() {
	w.flushes++
	w.ResponseRecorder.Flush()
	if w.flushes == 2 {
		w.cancel()
	}
}

func TestHubHandlerSendsKeepalivePing(t *testing.T) {
	hub := NewHub("keepalive-build")
	recorder := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &cancelAfterKeepaliveWriter{ResponseRecorder: recorder, cancel: cancel}
	done := make(chan struct{})
	go func() {
		hub.Handler()(writer, httptest.NewRequest(http.MethodGet, "/hmr/events", nil).WithContext(ctx))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(17 * time.Second):
		cancel()
		t.Fatal("handler did not flush a keepalive ping")
	}
	if !strings.Contains(recorder.Body.String(), ": keep-alive ") {
		t.Fatalf("SSE stream did not contain a keepalive comment: %q", recorder.Body.String())
	}
}

func TestHubHandlerExitsWhenSubscriberChannelCloses(t *testing.T) {
	hub := NewHub("closed-client-build")
	recorder := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		hub.Handler()(recorder, httptest.NewRequest(http.MethodGet, "/hmr/events", nil).WithContext(ctx))
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for hub.ClientCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if hub.ClientCount() != 1 {
		cancel()
		t.Fatal("handler did not register its SSE subscriber")
	}

	hub.mu.Lock()
	for client := range hub.clients {
		delete(hub.clients, client)
		close(client)
	}
	hub.mu.Unlock()

	select {
	case <-done:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("handler did not exit after its subscription channel closed")
	}
	if got := hub.ClientCount(); got != 0 {
		t.Fatalf("client count after closed subscription = %d, want 0", got)
	}
}

func TestHubSubscriptionCancelIsIdempotent(t *testing.T) {
	hub := NewHub("cancel-build")
	events, cancel := hub.Subscribe()
	cancel()
	cancel()
	if _, ok := <-events; ok {
		t.Fatal("subscription channel remained open after cancellation")
	}
	if got := hub.ClientCount(); got != 0 {
		t.Fatalf("client count after repeated cancel = %d, want 0", got)
	}
}
