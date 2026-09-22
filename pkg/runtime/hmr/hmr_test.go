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
package hmr

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	flatbuffers "github.com/google/flatbuffers/go"
)

func TestHubBroadcastToSubscribers(t *testing.T) {
	hub := NewHub("test-build")
	events, cancel := hub.Subscribe()
	defer cancel()

	if got := hub.ClientCount(); got != 1 {
		t.Fatalf("ClientCount() = %d, want 1", got)
	}

	hub.Broadcast(Event{Kind: EventReload, Source: "main.go"})

	select {
	case ev := <-events:
		if ev.Kind != EventReload {
			t.Errorf("Kind = %q, want %q", ev.Kind, EventReload)
		}
		if ev.Source != "main.go" {
			t.Errorf("Source = %q, want main.go", ev.Source)
		}
	default:
		t.Fatal("expected event to be delivered")
	}
}

func TestHubSlowClientIsNotBlocked(t *testing.T) {
	hub := NewHub("test-build")
	_, cancel := hub.Subscribe() // never drained
	defer cancel()

	// More events than the buffer holds must not block or panic.
	for i := 0; i < 32; i++ {
		hub.Broadcast(Event{Kind: EventPing})
	}
	if got := hub.ClientCount(); got != 1 {
		t.Errorf("ClientCount() = %d, want 1 (client stays connected)", got)
	}
}

func TestHubCancelClosesChannel(t *testing.T) {
	hub := NewHub("test-build")
	events, cancel := hub.Subscribe()
	cancel()

	if _, ok := <-events; ok {
		t.Error("expected channel to be closed after cancel")
	}
	if got := hub.ClientCount(); got != 0 {
		t.Errorf("ClientCount() = %d, want 0", got)
	}
}

// TestHubReplaysRecentEventToNewSubscriber covers the dev-mode restart
// race: the SSE connection drops before the watcher's broadcast happens,
// so a client subscribing moments later must still receive it.
func TestHubReplaysRecentEventToNewSubscriber(t *testing.T) {
	hub := NewHub("test-build")
	hub.Broadcast(Event{Kind: EventFragment, Route: "/about", Source: "about.templ"})

	events, cancel := hub.Subscribe()
	defer cancel()

	select {
	case ev := <-events:
		if ev.Route != "/about" {
			t.Errorf("replayed event Route = %q, want /about", ev.Route)
		}
	default:
		t.Fatal("expected the recent broadcast to be replayed to the new subscriber")
	}
}

func TestHubDoesNotReplayStaleEvent(t *testing.T) {
	hub := NewHub("test-build")
	hub.Broadcast(Event{Kind: EventReload, Source: "main.go"})
	hub.lastAt = hub.lastAt.Add(-replayWindow * 2)

	events, cancel := hub.Subscribe()
	defer cancel()

	select {
	case ev := <-events:
		t.Fatalf("expected no replay for a stale event, got %+v", ev)
	default:
	}
}

func TestWriteEventFormat(t *testing.T) {
	rec := &stringWriter{header: http.Header{}}
	want := Event{Kind: EventFragment, Target: "#hero", Payload: "<h1>a\nb</h1>", Source: "index.templ"}
	writeEvent(rec, want)

	out := rec.String()
	if !strings.HasPrefix(out, "event: fb\ndata: ") {
		t.Fatalf("unexpected SSE frame prefix: %q", out)
	}
	if !strings.HasSuffix(out, "\n\n") {
		t.Error("event must end with a blank line")
	}

	b64 := strings.TrimSuffix(strings.TrimPrefix(out, "event: fb\ndata: "), "\n\n")
	buf, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("data line is not valid base64: %v", err)
	}

	// The wire format is binary FlatBuffers, so a raw newline embedded in
	// the payload must survive untouched (no manual escaping needed).
	got := decodeEvent(buf)
	if got != want {
		t.Errorf("decodeEvent(encodeEvent(ev)) = %+v, want %+v", got, want)
	}
}

func TestEncodeDecodeEventRoundTrip(t *testing.T) {
	cases := []Event{
		{Kind: EventReload, Source: "main.go"},
		{Kind: EventPing},
		{Kind: EventFragment, Target: "#hero", Payload: "<h1>multi\nline\nhtml</h1>", Source: "index.templ"},
		{Kind: EventFragment, Target: "", Payload: "", Source: ""},
		{Kind: EventFragment, Target: "#fgoths-content", Payload: "<h1>About</h1>", Source: "about.templ", Route: "/about"},
	}
	for _, want := range cases {
		got := decodeEvent(encodeEvent(want))
		if got != want {
			t.Errorf("round trip mismatch: got %+v, want %+v", got, want)
		}
	}
}

func TestDecodeEventTruncatedBuffer(t *testing.T) {
	if got := decodeEvent(nil); got != (Event{}) {
		t.Errorf("decodeEvent(nil) = %+v, want zero value", got)
	}
	if got := decodeEvent([]byte{1, 2}); got != (Event{}) {
		t.Errorf("decodeEvent(short buffer) = %+v, want zero value", got)
	}
}

// TestDecodeEventMissingRouteField exercises the "field absent from
// vtable" branch in tableString by hand-building a legacy 4-field table
// (no Route) with the same builder primitives encodeEvent uses, mirroring
// what an older client would have sent before Route existed.
func TestDecodeEventMissingRouteField(t *testing.T) {
	b := flatbuffers.NewBuilder(64)
	kind := b.CreateString("reload")
	target := b.CreateString("")
	payload := b.CreateString("")
	source := b.CreateString("main.go")

	b.StartObject(4)
	b.PrependUOffsetTSlot(0, kind, 0)
	b.PrependUOffsetTSlot(1, target, 0)
	b.PrependUOffsetTSlot(2, payload, 0)
	b.PrependUOffsetTSlot(3, source, 0)
	root := b.EndObject()
	b.Finish(root)

	want := Event{Kind: "reload", Source: "main.go"}
	if got := decodeEvent(b.FinishedBytes()); got != want {
		t.Errorf("decodeEvent(legacy 4-field buffer) = %+v, want %+v", got, want)
	}
}

func TestHubHandlerRequiresFlusher(t *testing.T) {
	hub := NewHub("test-build")
	rec := &stringWriter{header: http.Header{}}
	req := httptest.NewRequest(http.MethodGet, "/hmr/events", nil)

	hub.Handler()(rec, req)

	if !strings.Contains(rec.String(), "streaming unsupported") {
		t.Errorf("expected streaming unsupported error, got %q", rec.String())
	}
}

func TestHubHandlerServesSSE(t *testing.T) {
	hub := NewHub("test-build")
	rec := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/hmr/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		hub.Handler()(rec, req)
		close(done)
	}()

	// Give the handler time to subscribe and write the hello frame before
	// broadcasting, and again before cancelling, so every write happens
	// strictly before we read rec.Body (after <-done) -- no data race.
	time.Sleep(30 * time.Millisecond)
	if got := hub.ClientCount(); got != 1 {
		t.Fatalf("ClientCount() while connected = %d, want 1", got)
	}
	hub.Broadcast(Event{Kind: EventReload, Source: "main.go"})
	time.Sleep(30 * time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancellation")
	}

	if got := hub.ClientCount(); got != 0 {
		t.Errorf("ClientCount() after handler returns = %d, want 0", got)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "retry: 1000") {
		t.Errorf("missing retry hint: %q", body)
	}
	if strings.Count(body, "event: fb") < 2 {
		t.Errorf("expected at least 2 fb frames (hello + broadcast), got: %q", body)
	}
}

func TestHubHandlerHelloEventIsVersioned(t *testing.T) {
	hub := NewHub("test-build")
	rec := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/hmr/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		hub.Handler()(rec, req)
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancellation")
	}

	ev := firstSSEEvent(t, rec.Body.String())
	if ev.Kind != EventPing {
		t.Fatalf("hello Kind = %q, want %q", ev.Kind, EventPing)
	}
	if !IsCompatible(ev.SchemaMajor, ev.SchemaMinor) {
		t.Fatalf("hello schema = %d.%d is not compatible", ev.SchemaMajor, ev.SchemaMinor)
	}
	if ev.BuildID != "test-build" {
		t.Fatalf("hello BuildID = %q, want test-build", ev.BuildID)
	}
	if ev.TS == 0 {
		t.Fatal("hello TS not stamped")
	}
}

func firstSSEEvent(t *testing.T, body string) Event {
	t.Helper()
	for _, frame := range strings.Split(body, "\n\n") {
		if !strings.HasPrefix(frame, "event: fb\ndata: ") {
			continue
		}
		b64 := strings.TrimPrefix(frame, "event: fb\ndata: ")
		buf, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Fatalf("hello data line is not valid base64: %v", err)
		}
		return decodeEvent(buf)
	}
	t.Fatal("missing fb event frame")
	return Event{}
}

func TestInjectClientScript(t *testing.T) {
	html := "<html><body><h1>hi</h1></body></html>"
	got := InjectClientScript(html)
	if !strings.Contains(got, `<script src="/hmr/hmr.js" defer></script>`) {
		t.Errorf("script tag not injected: %q", got)
	}
	if !strings.HasSuffix(got, "</body></html>") {
		t.Errorf("script should be inserted before </body>: %q", got)
	}
}

func TestInjectClientScriptNoBodyTag(t *testing.T) {
	html := "<div>no body here</div>"
	got := InjectClientScript(html)
	if !strings.HasSuffix(got, `<script src="/hmr/hmr.js" defer></script>`) {
		t.Errorf("expected script appended when no </body>, got %q", got)
	}
}

func TestScriptHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hmr/hmr.js", nil)

	ScriptHandler()(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want javascript", ct)
	}
	if !strings.Contains(rec.Body.String(), "fgoths_hmr") {
		t.Error("expected client script body to be served")
	}
}

// --- wire schema versioning (Bloco 1) ---

func TestIsCompatible(t *testing.T) {
	cases := []struct {
		major, minor uint16
		want         bool
	}{
		{SchemaMajor, SchemaMinor, true},
		{SchemaMajor, SchemaMinor - 1, true},  // older minor: additive fields ignored
		{SchemaMajor, 0, true},                // legacy buffer without any minor
		{SchemaMajor, SchemaMinor + 1, false}, // newer minor: unknown semantics
		{SchemaMajor + 1, 0, false},           // breaking major
		{SchemaMajor - 1, 0, false},
	}
	for _, tc := range cases {
		if got := IsCompatible(tc.major, tc.minor); got != tc.want {
			t.Errorf("IsCompatible(%d, %d) = %v, want %v", tc.major, tc.minor, got, tc.want)
		}
	}
}

func TestBroadcastStampsSchemaAndBuild(t *testing.T) {
	hub := NewHub("build-abc123")
	events, cancel := hub.Subscribe()
	defer cancel()

	hub.Broadcast(Event{Kind: EventReload, Source: "main.go"})

	select {
	case ev := <-events:
		if ev.SchemaMajor != SchemaMajor || ev.SchemaMinor != SchemaMinor {
			t.Errorf("schema version = %d.%d, want %d.%d", ev.SchemaMajor, ev.SchemaMinor, SchemaMajor, SchemaMinor)
		}
		if ev.BuildID != "build-abc123" {
			t.Errorf("BuildID = %q, want build-abc123", ev.BuildID)
		}
		if ev.TS == 0 {
			t.Error("TS not stamped on broadcast")
		}
	default:
		t.Fatal("expected event to be delivered")
	}
}

func TestBuildID(t *testing.T) {
	hub := NewHub("build-xyz")
	if got := hub.BuildID(); got != "build-xyz" {
		t.Errorf("BuildID() = %q, want build-xyz", got)
	}
}

func TestVersionedRoundTrip(t *testing.T) {
	want := Event{
		Kind:        EventFragment,
		Target:      "#fgoths-content",
		Payload:     "<p>v</p>",
		Source:      "view.templ",
		Route:       "/",
		SchemaMajor: SchemaMajor,
		SchemaMinor: SchemaMinor,
		BuildID:     "build-abc123",
		TS:          1730000000000000000,
	}
	got := decodeEvent(encodeEvent(want))
	if got != want {
		t.Errorf("versioned round trip mismatch: got %+v, want %+v", got, want)
	}
}

type stringWriter struct {
	b      strings.Builder
	header http.Header
}

func (s *stringWriter) Write(p []byte) (int, error) { return s.b.Write(p) }
func (s *stringWriter) Header() http.Header         { return s.header }
func (s *stringWriter) WriteHeader(int)             {}
func (s *stringWriter) String() string              { return s.b.String() }
