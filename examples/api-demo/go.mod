module api-demo

go 1.26.0

require (
	github.com/fsnotify/fsnotify v1.7.0
	github.com/google/flatbuffers v24.3.25+incompatible
)

require golang.org/x/sys v0.4.0 // indirect

// Metrics uses standard library — no external dependencies (Prometheus-compatible output)
// Health check uses standard library - no additional dependencies
