module {{.ProjectName}}

go 1.26.0

require (
        github.com/fsnotify/fsnotify v1.7.0
        github.com/google/flatbuffers v24.3.25+incompatible
)

{{- if .IsMVC }}
require github.com/a-h/templ v0.3.1020

tool github.com/a-h/templ/cmd/templ
{{- end }}

{{- if .UseSQLite }}
require (
	modernc.org/sqlite v1.34.4
)
{{- end }}

{{- if .Has "metrics" }}
// Metrics uses standard library — no external dependencies (Prometheus-compatible output)
{{- end }}

{{- if .Has "health" }}
// Health check uses standard library - no additional dependencies
{{- end }}

{{- if .Has "flatbuffers" }}
// FlatBuffers is already required above: the HMR module uses it for
// zero-copy event framing. This flag additionally scaffolds schemas/*.fbs
// for your own application data.
{{- end }}

{{- if .Has "htmx" }}
// HTMX is client-side - no Go dependencies needed
{{- end }}

{{- if .Has "jwt-auth" }}
require github.com/golang-jwt/jwt/v5 v5.3.1
{{- end }}

{{- if .Has "mtls" }}
// mTLS and identity-aware routing use crypto/tls and crypto/x509 from the standard library - no additional dependencies
{{- end }}

{{- if .Has "otel" }}
require (
	go.opentelemetry.io/otel v1.37.0
	go.opentelemetry.io/otel/trace v1.37.0
)
{{- end }}

{{- if .Has "grpc" }}
// gRPC-style serving uses the standard library (HTTP JSON transcoding) - no additional dependencies
{{- end }}
