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


ARG TARGETOS=linux
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
	go build -ldflags="-s -w" -trimpath -o /out/app .


RUN mkdir -p /out/data && chown -R 65532:65532 /out/data

FROM scratch

WORKDIR /app
ENV PORT=8080

ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build --chown=65532:65532 /out/data /data

COPY --from=build --chown=65532:65532 /out/app ./app

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["./app"]