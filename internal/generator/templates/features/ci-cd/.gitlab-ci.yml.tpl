stages:
  - lint
  - test
  - build

variables:
  GOFLAGS: "-mod=readonly"

lint:
  stage: lint
  image: golang:1.27
  script:
    - test -z "$(gofmt -l .)" || (gofmt -l . && exit 1)
    - go vet ./...
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
    - if: $CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH

test:
  stage: test
  script:
    - go test -race -coverprofile=coverage.out ./...
  artifacts:
    paths:
      - coverage.out
    expire_in: 7 days
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
    - if: $CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH

build:
  stage: build
  script:
    - CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o bin/app ./cmd/app
  artifacts:
    paths:
      - bin/app
    expire_in: 7 days
  rules:
    - if: $CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH
