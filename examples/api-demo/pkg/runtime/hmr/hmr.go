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
// Package hmr implements the FGOTHS hot module replacement hub: a
// stdlib-only Server-Sent Events broadcaster that pushes change events to
// connected browsers during development. Events are framed as FlatBuffers
// (see codec.go) and transported base64-encoded over SSE (text/event-stream)
// data lines, giving zero-copy reads on both ends without a flatc/C++
// toolchain dependency.
package hmr

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Event kinds pushed to browsers.
const (
	EventReload   = "reload"   // full page reload (Go code or layout changed)
	EventFragment = "fragment" // targeted fragment swap (data changed)
	EventPing     = "ping"     // keep-alive
)

// Wire schema version of the HMR event protocol. Major bumps are breaking
// (the client must not interpret a foreign-major buffer); minor bumps are
// additive and ignored by older clients. These are stamped on every
// broadcast and checked by the browser client before dispatching.
const (
	SchemaMajor = 1
	SchemaMinor = 1
)

// IsCompatible reports whether an event carrying (major, minor) can be
// dispatched by a client built against the current schema constants: same
// major, and minor at most the client's own minor (an older client ignores
// additive fields it does not know; a newer minor may carry semantics it
// cannot handle, so it is rejected in favor of a safe reload).
func IsCompatible(major, minor uint16) bool {
	return major == SchemaMajor && minor <= SchemaMinor
}

// Event is a change notification delivered to every connected browser.
type Event struct {
	// Kind is one of EventReload, EventFragment or EventPing.
	Kind string
	// Target is the CSS selector of the fragment to swap (EventFragment
	// only). Empty for reload events.
	Target string
	// Payload is the new HTML fragment (EventFragment) or empty.
	Payload string
	// Source describes what changed, for the dev console log.
	Source string
	// Route is the page path the fragment was rendered for (EventFragment
	// only). The client only applies the patch if it matches the current
	// page, so a fragment meant for /about never overwrites /.
	Route string
	// SchemaMajor/SchemaMinor identify the wire schema the event was
	// encoded against (stamped by Hub.Broadcast).
	SchemaMajor uint16
	SchemaMinor uint16
	// BuildID identifies the dev build that emitted the event.
	BuildID string
	// TS is the emission timestamp as unix nanoseconds.
	TS uint64
}

// Hub broadcasts events to all connected SSE clients. A zero Hub is ready
// to use. Hub is safe for concurrent use.
type Hub struct {
	mu      sync.Mutex
	clients map[chan Event]struct{}
	last    *Event
	lastAt  time.Time
	buildID string
}

// replayWindow bounds how long a freshly (re)connected client is replayed
// the last broadcast event. A dev-mode restart always drops the SSE
// connection before the watcher's broadcast can be delivered, so without
// a replay the reconnecting client would silently miss it. Bounding the
// window keeps a brand-new tab opened long after the last change from
// getting a stale, pointless replay.
const replayWindow = 2 * time.Second

// NewHub returns an empty event hub. buildID stamps every broadcast with
// an identity for the current dev build (e.g. a timestamp or git sha).
func NewHub(buildID string) *Hub {
	return &Hub{clients: make(map[chan Event]struct{}), buildID: buildID}
}

// BuildID returns the build identity stamped on every broadcast.
func (h *Hub) BuildID() string {
	return h.buildID
}

// Subscribe registers a new client and returns its event channel plus a
// cancel function that must be called when the client disconnects. If the
// hub broadcast an event within replayWindow (typically because a restart
// dropped every connection right before this one was established), that
// event is replayed to the new client immediately.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 8)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	var replay *Event
	if h.last != nil && time.Since(h.lastAt) < replayWindow {
		ev := *h.last
		replay = &ev
	}
	h.mu.Unlock()
	if replay != nil {
		ch <- *replay
	}
	cancel := func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

// Broadcast delivers the event to every connected client and remembers it
// for replayWindow so clients that reconnect moments later (e.g. right
// after a dev-mode process restart) still receive it. Slow clients (full
// buffer) are dropped rather than blocking the publisher. The event is
// stamped with the hub's build identity and the current wire-schema
// version before fan-out.
func (h *Hub) Broadcast(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ev = h.stamp(ev)
	h.last = &ev
	h.lastAt = time.Now()
	for ch := range h.clients {
		select {
		case ch <- ev:
		default:
			// Client is too slow; drop the event for it.
		}
	}
}

func (h *Hub) stamp(ev Event) Event {
	ev.SchemaMajor = SchemaMajor
	ev.SchemaMinor = SchemaMinor
	ev.BuildID = h.buildID
	ev.TS = uint64(time.Now().UnixNano())
	return ev
}

// ClientCount returns the number of connected browsers (useful for dev
// diagnostics).
func (h *Hub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Handler returns the SSE endpoint. Browsers connect with EventSource and
// receive change events until they disconnect.
func (h *Hub) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		events, cancel := h.Subscribe()
		defer cancel()

		// Initial retry hint and a hello event so the client knows the
		// stream is live.
		_, _ = fmt.Fprint(w, "retry: 1000\n\n")
		writeEvent(w, h.stamp(Event{Kind: EventPing, Source: "connected"}))
		flusher.Flush()

		ping := time.NewTicker(15 * time.Second)
		defer ping.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ping.C:
				_, _ = fmt.Fprintf(w, ": keep-alive %d\n\n", time.Now().UnixNano())
				flusher.Flush()
			case ev, ok := <-events:
				if !ok {
					return
				}
				writeEvent(w, ev)
				flusher.Flush()
			}
		}
	}
}

// writeEvent serializes an Event as a FlatBuffers table and transmits it as
// a single base64-encoded SSE data line. Every event is sent under the
// generic "fb" SSE event name; the client decodes the buffer and dispatches
// on the embedded Kind field instead of relying on the SSE event name,
// which keeps the framing binary-safe (no newline escaping of HTML needed).
func writeEvent(w http.ResponseWriter, ev Event) {
	buf := encodeEvent(ev)
	_, _ = fmt.Fprintf(w, "event: fb\ndata: %s\n\n", base64.StdEncoding.EncodeToString(buf))
}
