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
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// HealthCheck is a probe used by readiness checks to validate a dependency
// (e.g. database, cache, upstream) before the server reports itself ready.
type HealthCheck func(ctx context.Context) error

// AlertCallback receives a metrics snapshot and the computed error rate when
// the configured alert threshold is crossed.
type AlertCallback func(snapshot MetricsSnapshot, errorRate float64)

// WithLiveness registers a liveness endpoint that always reports healthy while
// the process is running. It signals that the server is alive, not that its
// dependencies are ready.
func (s *Server) WithLiveness(path string) *Server {
	if s == nil || s.router == nil {
		return s
	}
	if path == "" {
		path = "/health/live"
	}
	s.router.Get(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	return s
}

// WithReadinessTimeout sets the per-request timeout applied to readiness
// checks. Defaults to 2 seconds when not configured.
func (s *Server) WithReadinessTimeout(timeout time.Duration) *Server {
	if s == nil {
		return nil
	}
	s.readinessTimeout = timeout
	return s
}

// WithReadiness registers a readiness endpoint that runs the given checks and
// reports 503 if any of them fail or time out.
func (s *Server) WithReadiness(path string, checks ...HealthCheck) *Server {
	if s == nil || s.router == nil {
		return s
	}
	if path == "" {
		path = "/health/ready"
	}
	timeout := s.readinessTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	s.router.Get(path, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		for _, check := range checks {
			if check == nil {
				continue
			}
			if err := check(ctx); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "unavailable", "error": err.Error()})
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	return s
}

type alertThreshold struct {
	mu        sync.Mutex
	threshold float64
	callback  AlertCallback
	triggered bool
}

// WithAlertThreshold enables a lightweight operational alert that fires the
// callback once the sliding-window error rate crosses the given threshold
// (0.0-1.0). The rate is computed over the last 1000 requests (bounded
// window), not the cumulative process lifetime — cumulative rates dilute
// spikes and never fire on long-lived services. Requires WithMetrics to be
// enabled first. This is intentionally simple: it is not a replacement for a
// real SLO/alerting stack, just an opt-in hook so generated projects can
// react to degraded error rates.
func (s *Server) WithAlertThreshold(threshold float64, callback AlertCallback) *Server {
	if s == nil || s.metrics == nil || callback == nil {
		return s
	}
	alert := &alertThreshold{threshold: threshold, callback: callback}
	s.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorder := newStatusRecorder(w)
			next.ServeHTTP(recorder, r)

			// Bounded-window rate: O(1) per request (ring buffer read),
			// no full Snapshot clone under the metrics lock.
			_, errorRate := s.metrics.ErrorWindow(errorWindowCap)

			alert.mu.Lock()
			defer alert.mu.Unlock()
			if errorRate >= alert.threshold && !alert.triggered {
				alert.triggered = true
				snapshot := s.metrics.Snapshot()
				alert.callback(snapshot, errorRate)
			} else if errorRate < alert.threshold {
				alert.triggered = false
			}
		})
	})
	return s
}
