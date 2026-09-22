# FGOTHS Architecture & Boilerplate Spec

This document details the internal layout, folder structure, and runtime data flow of the **FGOTHS** framework ecosystem.

---

## 🏗 System Architecture Diagram

```mermaid
graph TD
    User([Browser / Client]) -->|HTTP Request| Proxy

    subgraph Binary [FGOTHS Single Binary Static Executable]
        subgraph ProxyLayer [Embedded Proxy Engine]
            Proxy[Pure Go Reverse Proxy]
            FBSConfig[(Optional .fbs Generated Types)]
            Proxy <-->|Optional Application Configuration| FBSConfig
        end

        subgraph CoreApp [Application Runtime]
            Router[Net/HTTP Dispatcher]
            Handler[Go Business Handler]
            DB[(SQL Engine Driver)]
            TemplEngine[Templ Engine Compiles HTML]
        end

        subgraph EmbeddedAssets [Optional Embedded Assets]
            HTMXScript[Optional HTMX JS]
        end

        Proxy -->|HTTP forwarding| Router
        Router --> Handler
        Handler -->|Queries| DB
        Handler -->|Passes Structs| TemplEngine
        TemplEngine -->|Injects Assets| EmbeddedAssets
    end

    TemplEngine -->|Server-Side Rendered HTML| User

    📂 Project Directory Scaffold
    Below is the standard repository structure for developing and maintaining the FGOTHS framework and CLI engine:

    fgoths-framework/
    ├── .github/
    │   └── workflows/
    │       └── ci.yml               # Framework CI (fmt, vet, race tests)
    ├── cmd/
    │   └── fgoths/                 # CLI Binary Entrypoint
    │       └── main.go
    ├── internal/
    │   ├── cli/                     # CLI Subcommand Implementations
    │   │   ├── build.go             # Static Cross-Compilation & Scratch Packaging
    │   │   ├── controlplane.go      # Governance REST API launcher
    │   │   ├── crud.go              # `generate crud` scaffold command
    │   │   ├── generate.go          # Pipeline: Templ compilation (webapp/SSR projects)
    │   │   ├── init.go              # Project Architecture Initializer
    │   │   ├── root.go              # Argument Parsing & Command Routing
    │   │   └── sync_templates.go    # Runtime template drift guard (dev tool)
    │   ├── config/                  # Project configuration types, presets, validation
    │   ├── generator/               # Embedded Template Generator
    │   │   ├── generator.go         # Staging-dir writer (atomic generation)
    │   │   └── templates/           # Scaffolding Blueprints (go:embed)
    │   │       ├── base/            # Always-generated files (go.mod, Makefile, runtime)
    │   │       ├── architectures/  # flat / mvc layouts
    │   │       ├── database/       # sqlite / postgres / mysql layers + migrations
    │   │       └── features/       # jwt-auth, otel, mtls, grpc, openapi, health,
    │   │                           # metrics, htmx, flatbuffers, ci-cd
    ├── pkg/
    │   └── runtime/                 # Public SDK copied verbatim into generated apps
    │       ├── server.go            # High-Performance HTTP Engine + Proxy
    │       ├── auth.go              # JWT policies (RBAC + ABAC)
    │       ├── otel.go              # OpenTelemetry tracing
    │       ├── tls.go               # Mutual TLS & upstream TLS
    │       ├── controlplane.go      # Control Plane REST API
    │       ├── governance.go        # Deployment ledger & release workflows
    │       └── ...                  # metrics, health, rollout, secrets, tenant
    ├── benchmarks/                  # Router comparison suite + stress scripts
    ├── examples/
    │   └── proxy-demo/              # Minimal reverse-proxy example
    ├── docs/
    │   └── adr/                     # Architecture Decision Records
    │       └── 0001-native-go-proxy-over-envoy.md
    ├── ARCHITECTURE.md              # Technical Architecture Spec
    ├── BOILERPLATE.md               # Folder Structure & Flow Diagrams
    ├── CODE_OF_CONDUCT.md           # Contributor Covenant 2.1
    ├── CONTRIBUTING.md              # Developer Setup & PR Guidelines
    ├── LICENSE                      # Apache 2.0 License
    ├── Makefile                     # Local Development Automation
    ├── README.md                    # Project Landing Page on GitHub
    ├── go.mod
    └── go.sum

  🔄 Lifecycle Execution Flow
  	1.	CLI Scaffolding (init):
    The user executes fgoths init --preset=webapp --name=my-app. The CLI unpacks the embedded templates (go:embed) and provisions a workspace with independent go.mod, optional .fbs schemas, and Templ components.
  	2.	Compilation Pipeline (generate):
    ⚬ FlatBuffers (.fbs) are compiled into Go structs when selected.
    ⚬ Templ files are pre-rendered into static .go code when present.
    ⚬ Frontend CSS tooling remains the consuming application's responsibility.
  	3.	Runtime Execution:
    Upon startup, the embedded pure Go runtime dispatches HTTP requests to Go handlers, which may query SQL and stream HTML through the selected rendering stack.
  	4.	Container Build (build):
      The application is compiled static (CGO_ENABLED=0), outputting a single executable image targeting a bare Docker scratch container.
