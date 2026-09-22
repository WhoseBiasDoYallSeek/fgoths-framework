# Proxy + observability demo

This example shows the FGOTHS native runtime in a realistic gateway pattern:

- local upstream service on `:18080`
- reverse proxy on `:8080`
- prefix stripping (`/gateway` → upstream root)
- retry + circuit breaker
- request observer for latency and status tracking
- opt-in in-memory metrics on both server and proxy
- opt-in structured request logging (`slog`)
- liveness/readiness probes with a sample dependency check
- a lightweight error-rate alert hook

## Run

```bash
go run ./examples/proxy-demo
```

Then call the endpoints:

```bash
curl http://localhost:8080/health
curl http://localhost:8080/health/live
curl http://localhost:8080/health/ready
curl http://localhost:8080/metrics
curl http://localhost:8080/gateway/orders/42
```

The last call is proxied to the upstream service and logs the timing + status from the observer middleware and the structured logger. `/metrics` exposes the in-memory request counters collected by `Server.WithMetrics()`.
