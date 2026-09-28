// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package runtime

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestServerWithListenerServesOnInjectedListener verifies that WithListener
// makes the next ListenAndServe serve on the pre-created listener instead of
// binding a new one — the socket-activation / systemd handoff path.
func TestServerWithListenerServesOnInjectedListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pre-create listener: %v", err)
	}
	defer ln.Close()

	srv := NewServer("127.0.0.1:0").WithListener(ln)
	srv.HandleFunc(http.MethodGet, "/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	go func() { _ = srv.ListenAndServe() }()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("request on injected listener: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown() error = %v", err)
	}
}

// TestServerWithListenerNilIsNoOp verifies the nil-listener guard.
func TestServerWithListenerNilIsNoOp(t *testing.T) {
	srv := NewServer("127.0.0.1:0")
	if got := srv.WithListener(nil); got != srv {
		t.Error("WithListener(nil) must return the same server")
	}
	if srv.injectedListener != nil {
		t.Error("WithListener(nil) must not set an injected listener")
	}
	if got := (*Server)(nil).WithListener(nil); got != nil {
		t.Error("WithListener on nil server must return nil")
	}
}

// TestServerWithListenerConsumedOnce verifies the listener is consumed by the
// first ListenAndServe and a second call binds normally (or fails on a busy
// port), never reusing the injected one.
func TestServerWithListenerConsumedOnce(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pre-create listener: %v", err)
	}
	defer ln.Close()

	srv := NewServer("127.0.0.1:0").WithListener(ln)
	got, err := srv.listen()
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	if got != ln {
		t.Error("first listen must return the injected listener")
	}
	second, err := srv.listen()
	if err != nil {
		t.Fatalf("second listen (fresh bind): %v", err)
	}
	second.Close()
	if second == ln {
		t.Error("second listen must not return the consumed injected listener")
	}
}

// TestServerListenWithoutInitializedServer verifies the error path when
// listen is called before the http.Server is initialized.
func TestServerListenWithoutInitializedServer(t *testing.T) {
	srv := &Server{Server: nil}
	if _, err := srv.listen(); err == nil {
		t.Error("expected error when http.Server is not initialized")
	}
}

// TestListenAndServeOnUninitializedServer verifies the guard returns
// http.ErrServerClosed instead of panicking.
func TestListenAndServeOnUninitializedServer(t *testing.T) {
	if err := (*Server)(nil).ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("nil server ListenAndServe = %v, want http.ErrServerClosed", err)
	}
}

// TestListenAndServeBindErrorSurfaces verifies a bind failure is returned
// to the caller instead of being swallowed.
func TestListenAndServeBindErrorSurfaces(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("blocker listen: %v", err)
	}
	defer blocker.Close()

	srv := NewServer(blocker.Addr().String())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected bind error, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ListenAndServe did not return after bind failure")
	}
}

// TestListenAndServeWritesReadyFile verifies the FGOTHS_READY_FILE handshake
// used by the dev watcher to detect that the freshly built binary is serving.
func TestListenAndServeWritesReadyFile(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pre-create listener: %v", err)
	}
	defer ln.Close()

	readyPath := filepath.Join(t.TempDir(), "ready")
	t.Setenv("FGOTHS_READY_FILE", readyPath)
	t.Setenv("FGOTHS_BUILD_ID", "build-42")

	srv := NewServer("127.0.0.1:0").WithListener(ln)
	go func() { _ = srv.ListenAndServe() }()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + ln.Addr().String() + "/")
	if err == nil {
		resp.Body.Close()
	}

	data, rerr := os.ReadFile(readyPath)
	if rerr != nil {
		t.Fatalf("ready file not written: %v", rerr)
	}
	if string(data) != "build-42" {
		t.Errorf("ready file content = %q, want %q", data, "build-42")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// TestOnShutdownNilHookIsNoOp verifies the nil-hook guard.
func TestOnShutdownNilHookIsNoOp(t *testing.T) {
	srv := NewServer("127.0.0.1:0")
	if got := srv.OnShutdown(nil); got != srv {
		t.Error("OnShutdown(nil) must return the same server")
	}
	if len(srv.onShutdown) != 0 {
		t.Error("OnShutdown(nil) must not register a hook")
	}
	if got := (*Server)(nil).OnShutdown(nil); got != nil {
		t.Error("OnShutdown on nil server must return nil")
	}
}

// TestWithShutdownTimeoutNilReceiver verifies the nil-receiver guard.
func TestWithShutdownTimeoutNilReceiver(t *testing.T) {
	if got := (*Server)(nil).WithShutdownTimeout(time.Second); got != nil {
		t.Error("WithShutdownTimeout on nil server must return nil")
	}
}

// TestWithReusePortNilReceiver verifies the nil-receiver guard.
func TestWithReusePortNilReceiver(t *testing.T) {
	if got := (*Server)(nil).WithReusePort(); got != nil {
		t.Error("WithReusePort on nil server must return nil")
	}
}

// TestStatusRecorderPoolRoundTrip verifies pooled recorders are reset between
// uses — a recycled wrapper must not leak a previous request's writer or
// status into the next one.
func TestStatusRecorderPoolRoundTrip(t *testing.T) {
	first := httptest.NewRecorder()
	sr1 := newStatusRecorder(first)
	sr1.WriteHeader(http.StatusTeapot)
	releaseStatusRecorder(sr1)

	second := httptest.NewRecorder()
	sr2 := newStatusRecorder(second)
	if sr2.status != http.StatusOK || sr2.wroteCode {
		t.Errorf("recycled recorder not reset: status=%d wroteCode=%v", sr2.status, sr2.wroteCode)
	}
	if sr2.ResponseWriter != http.ResponseWriter(second) {
		t.Error("recycled recorder must bind to the new writer")
	}
	releaseStatusRecorder(sr2)

	releaseStatusRecorder(nil) // must not panic
}

// TestNewServerSetsReadHeaderTimeout guards the Slowloris mitigation: the
// default server must bound the header-read phase.
func TestNewServerSetsReadHeaderTimeout(t *testing.T) {
	srv := NewServer(":0")
	if srv.ReadHeaderTimeout != defaultReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %v, want %v", srv.ReadHeaderTimeout, defaultReadHeaderTimeout)
	}
}
