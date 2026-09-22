# Contributing to FGOTHS

Thank you for your interest in contributing to FGOTHS! We welcome pull requests, bug reports, feature proposals, and documentation improvements.

---

## Development Setup

### Prerequisites
* **Go** 1.26 or higher
* **Git**

### Building from Source
```bash
# Clone repository
git clone https://github.com/WhoseBiasDoYallSeek/fgoths-framework.git
cd fgoths-framework

# Run unit tests
go test ./...

# Build CLI locally
go build -o fgoths cmd/fgoths/main.go
