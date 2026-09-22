package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"{{.ProjectName}}/pkg/runtime"
)

// Handler returns an HTTP handler that exposes metrics in Prometheus text format.
// No external dependencies — output is compatible with Prometheus scrape.
func Handler(srv *runtime.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		m := srv.Metrics()
		if m == nil {
			return
		}
		snap := m.Snapshot()

		fmt.Fprintf(w, "# HELP http_requests_total Total HTTP requests received\n")
		fmt.Fprintf(w, "# TYPE http_requests_total counter\n")
		fmt.Fprintf(w, "http_requests_total %d\n", snap.TotalRequests)

		fmt.Fprintf(w, "# HELP http_requests_errors_total Total HTTP requests that returned 4xx or 5xx\n")
		fmt.Fprintf(w, "# TYPE http_requests_errors_total counter\n")
		fmt.Fprintf(w, "http_requests_errors_total %d\n", snap.TotalErrors)

		fmt.Fprintf(w, "# HELP http_request_duration_milliseconds_total Total HTTP request duration in milliseconds\n")
		fmt.Fprintf(w, "# TYPE http_request_duration_milliseconds_total counter\n")
		fmt.Fprintf(w, "http_request_duration_milliseconds_total %.3f\n", float64(snap.TotalLatency.Milliseconds()))

		// Global latency percentiles
		gp := srv.Metrics().GlobalPercentiles()
		fmt.Fprintf(w, "# HELP http_request_duration_seconds_global HTTP request duration global percentiles\n")
		fmt.Fprintf(w, "# TYPE http_request_duration_seconds_global gauge\n")
		fmt.Fprintf(w, "http_request_duration_seconds_global{quantile=\"0.5\"} %.6f\n", gp.P50.Seconds())
		fmt.Fprintf(w, "http_request_duration_seconds_global{quantile=\"0.95\"} %.6f\n", gp.P95.Seconds())
		fmt.Fprintf(w, "http_request_duration_seconds_global{quantile=\"0.99\"} %.6f\n", gp.P99.Seconds())

		if len(snap.ByMethod) > 0 {
			fmt.Fprintf(w, "# HELP http_requests_by_method_total HTTP requests by HTTP method\n")
			fmt.Fprintf(w, "# TYPE http_requests_by_method_total counter\n")
			methods := sortedStringKeys(snap.ByMethod)
			for _, method := range methods {
				fmt.Fprintf(w, "http_requests_by_method_total{method=%q} %d\n", method, snap.ByMethod[method])
			}
		}

		if len(snap.ByRoute) > 0 {
			fmt.Fprintf(w, "# HELP http_requests_by_route_total HTTP requests by route pattern\n")
			fmt.Fprintf(w, "# TYPE http_requests_by_route_total counter\n")
			routes := sortedStringKeys(snap.ByRoute)
			for _, route := range routes {
				fmt.Fprintf(w, "http_requests_by_route_total{route=%q} %d\n", route, snap.ByRoute[route])
			}
		}

		if len(snap.ByStatus) > 0 {
			fmt.Fprintf(w, "# HELP http_requests_by_status_total HTTP requests by status code\n")
			fmt.Fprintf(w, "# TYPE http_requests_by_status_total counter\n")
			codes := sortedIntKeys(snap.ByStatus)
			for _, code := range codes {
				fmt.Fprintf(w, "http_requests_by_status_total{status=%d} %d\n", code, snap.ByStatus[code])
			}
		}

		// Per-route latency percentiles (histogram)
		if len(snap.ByRoutePercentiles) > 0 {
			fmt.Fprintf(w, "# HELP http_request_duration_seconds_route HTTP request duration percentiles by route\n")
			fmt.Fprintf(w, "# TYPE http_request_duration_seconds_route gauge\n")
			routes := sortedStringKeysOfPercentiles(snap.ByRoutePercentiles)
			for _, route := range routes {
				p := snap.ByRoutePercentiles[route]
				fmt.Fprintf(w, "http_request_duration_seconds_route{route=%q,quantile=\"0.5\"} %.6f\n", route, p.P50.Seconds())
				fmt.Fprintf(w, "http_request_duration_seconds_route{route=%q,quantile=\"0.95\"} %.6f\n", route, p.P95.Seconds())
				fmt.Fprintf(w, "http_request_duration_seconds_route{route=%q,quantile=\"0.99\"} %.6f\n", route, p.P99.Seconds())
			}
		}
	}
}

// Middleware records metrics for each request.
// Returns a middleware function that wraps the server's metrics collector.
func Middleware(srv *runtime.Server) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rec, r)
			srv.Metrics().Record(r.Method, r.URL.Path, rec.statusCode, time.Since(start))
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	if sr.statusCode == 0 {
		sr.statusCode = code
	}
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if sr.statusCode == 0 {
		sr.statusCode = http.StatusOK
	}
	return sr.ResponseWriter.Write(b)
}

func sortedStringKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedIntKeys(m map[int]int64) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

func sortedStringKeysOfPercentiles(m map[string]runtime.DurationPercentiles) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
