# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

# CA certificates and timezone data are copied into the final scratch image.
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /src

# Download dependencies in an isolated layer. `make docker-build` depends on
# `make generate`, so go.sum is present before Docker starts.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
{{if .IsMVC}}
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	go tool templ generate
{{end}}

ARG TARGETOS=linux
ARG TARGETARCH
{{if .IsMVC}}
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
	go build -ldflags="-s -w" -trimpath -o /out/app .
{{else}}
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
	go build -ldflags="-s -w" -trimpath -o /out/app .
{{end}}

RUN mkdir -p /out/data && chown -R 65532:65532 /out/data

FROM scratch

WORKDIR /app
ENV PORT=8080
{{if .UseSQLite}}
ENV DATABASE_URL="file:/data/app.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
{{end}}
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build --chown=65532:65532 /out/data /data
{{if .IsMVC}}
COPY --from=build --chown=65532:65532 /src/static ./static
{{end}}
COPY --from=build --chown=65532:65532 /out/app ./app

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["./app"]