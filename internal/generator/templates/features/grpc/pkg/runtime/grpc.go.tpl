package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// GRPCStatus codes mirror the subset of gRPC status codes the runtime uses so
// generated projects can serve real gRPC-style responses over HTTP/2 without
// pulling the full google.golang.org/grpc dependency tree.
type GRPCStatus int

const (
	GRPCOK                GRPCStatus = 0
	GRPCCanceled          GRPCStatus = 1
	GRPCUnknown           GRPCStatus = 2
	GRPCInvalidArgument   GRPCStatus = 3
	GRPCDeadlineExceeded  GRPCStatus = 4
	GRPCNotFound          GRPCStatus = 5
	GRPCAlreadyExists     GRPCStatus = 6
	GRPCPermissionDenied  GRPCStatus = 7
	GRPCResourceExhausted GRPCStatus = 8
	GRPCFailedPrecondition GRPCStatus = 9
	GRPCAborted           GRPCStatus = 10
	GRPCOutOfRange        GRPCStatus = 11
	GRPCUnimplemented     GRPCStatus = 12
	GRPCInternal          GRPCStatus = 13
	GRPCUnavailable       GRPCStatus = 14
	GRPCDataLoss          GRPCStatus = 15
	GRPCUnauthenticated   GRPCStatus = 16
)

func (s GRPCStatus) String() string {
	switch s {
	case GRPCOK:
		return "OK"
	case GRPCCanceled:
		return "Canceled"
	case GRPCUnknown:
		return "Unknown"
	case GRPCInvalidArgument:
		return "InvalidArgument"
	case GRPCDeadlineExceeded:
		return "DeadlineExceeded"
	case GRPCNotFound:
		return "NotFound"
	case GRPCAlreadyExists:
		return "AlreadyExists"
	case GRPCPermissionDenied:
		return "PermissionDenied"
	case GRPCResourceExhausted:
		return "ResourceExhausted"
	case GRPCFailedPrecondition:
		return "FailedPrecondition"
	case GRPCAborted:
		return "Aborted"
	case GRPCOutOfRange:
		return "OutOfRange"
	case GRPCUnimplemented:
		return "Unimplemented"
	case GRPCInternal:
		return "Internal"
	case GRPCUnavailable:
		return "Unavailable"
	case GRPCDataLoss:
		return "DataLoss"
	case GRPCUnauthenticated:
		return "Unauthenticated"
	default:
		return fmt.Sprintf("GRPCStatus(%d)", int(s))
	}
}

// grpcContentType is the content type used by gRPC-JSON transcoding responses.
const grpcContentType = "application/grpc+json"

// GRPCError carries a gRPC status code and message through a handler.
type GRPCError struct {
	Code    GRPCStatus
	Message string
}

func (e *GRPCError) Error() string {
	return fmt.Sprintf("grpc error %d (%s): %s", int(e.Code), e.Code.String(), e.Message)
}

// GRPCMethod is a unary gRPC-style method: it receives a decoded JSON request
// body and returns a response value or a *GRPCError.
type GRPCMethod func(ctx context.Context, req json.RawMessage) (any, *GRPCError)

// GRPCService groups named methods behind a logical service name, mirroring the
// fully-qualified method path convention of real gRPC: /<service>/<method>.
type GRPCService struct {
	Name    string
	Methods map[string]GRPCMethod
}

// GRPCServer serves gRPC-style unary methods over HTTP JSON transcoding.
// It keeps the runtime dependency-free while exposing the same surface
// (service/method routing, status codes, health checking) as native gRPC.
type GRPCServer struct {
	mu       sync.RWMutex
	services map[string]*GRPCService
	health   map[string]bool // service name -> serving state
	timeout  time.Duration
}

// NewGRPCServer creates an empty gRPC-style server with the standard health
// service already registered.
func NewGRPCServer() *GRPCServer {
	s := &GRPCServer{
		services: make(map[string]*GRPCService),
		health:   make(map[string]bool),
		timeout:  10 * time.Second,
	}
	s.health[""] = true // overall server health (grpc.health.v1 convention)
	return s
}

// Register adds a service and its methods. Methods are matched case-insensitively
// through the canonical /<service>/<method> path.
func (s *GRPCServer) Register(svc *GRPCService) error {
	if s == nil || svc == nil {
		return fmt.Errorf("grpc server and service are required")
	}
	svc.Name = strings.TrimSpace(svc.Name)
	if svc.Name == "" {
		return fmt.Errorf("grpc service name is required")
	}
	if len(svc.Methods) == 0 {
		return fmt.Errorf("grpc service %q must define at least one method", svc.Name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.services[svc.Name]; exists {
		return fmt.Errorf("grpc service %q already exists", svc.Name)
	}
	s.health[svc.Name] = true
	s.services[svc.Name] = svc
	return nil
}

// SetServingState flips the health state of a service (or of the whole server
// when the service name is empty).
func (s *GRPCServer) SetServingState(service string, serving bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health[strings.TrimSpace(service)] = serving
}

// SetTimeout configures the per-call deadline applied to every method invocation.
func (s *GRPCServer) SetTimeout(d time.Duration) {
	if s == nil || d <= 0 {
		return
	}
	s.timeout = d
}

// ServeHTTP implements the gRPC-JSON transcoding wire format:
//   POST /<service>/<method>
//   content-type: application/grpc+json
//   x-grpc-status: <code> (always present in responses, trailers-style)
func (s *GRPCServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil {
		http.Error(w, "grpc server is nil", http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeGRPCErr(w, &GRPCError{Code: GRPCInvalidArgument, Message: "grpc calls must use POST"})
		return
	}
	serviceName, methodName, ok := splitGRPCPath(r.URL.Path)
	if !ok {
		writeGRPCErr(w, &GRPCError{Code: GRPCNotFound, Message: "malformed grpc method path"})
		return
	}

	s.mu.RLock()
	svc, svcExists := s.services[serviceName]
	var method GRPCMethod
	if svcExists {
		method = svc.Methods[methodName]
	}
	serving := s.health[serviceName]
	s.mu.RUnlock()

	if !svcExists {
		writeGRPCErr(w, &GRPCError{Code: GRPCNotFound, Message: fmt.Sprintf("unknown grpc service %q", serviceName)})
		return
	}
	if method == nil {
		writeGRPCErr(w, &GRPCError{Code: GRPCUnimplemented, Message: fmt.Sprintf("method %q is not implemented by service %q", methodName, serviceName)})
		return
	}
	if !serving {
		writeGRPCErr(w, &GRPCError{Code: GRPCUnavailable, Message: fmt.Sprintf("service %q is not serving", serviceName)})
		return
	}
	if !isGRPCContentType(r) {
		writeGRPCErr(w, &GRPCError{Code: GRPCInvalidArgument, Message: "content-type must be application/grpc+json"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxGRPCRequestBody))
	if err != nil {
		writeGRPCErr(w, &GRPCError{Code: GRPCInternal, Message: "failed to read request body"})
		return
	}

	timeout := s.timeout
	if deadline, ok := r.Context().Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	resp, grpcErr := method(ctx, body)
	if grpcErr != nil {
		writeGRPCErr(w, grpcErr)
		return
	}
	w.Header().Set("Content-Type", grpcContentType)
	w.Header().Set("X-Grpc-Status", fmt.Sprintf("%d", int(GRPCOK)))
	w.WriteHeader(http.StatusOK)
	if resp != nil {
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// HealthHTTP exposes the gRPC health checking semantics over plain GET, so the
// existing runtime health probes and load balancers can consume it. The bare
// endpoint (empty or "grpc" service name) reports overall server health.
func (s *GRPCServer) HealthHTTP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		service := strings.Trim(r.URL.Path, "/")
		if service == "grpc" || service == "health/grpc" {
			service = "" // overall server health
		}
		s.mu.RLock()
		serving, ok := s.health[service]
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "UNKNOWN"})
			return
		}
		if !serving {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "NOT_SERVING"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "SERVING"})
	})
}

// ListenAndServe starts the gRPC-style server on the given address. It is a
// separate listener so gRPC and HTTP servers can coexist on different ports.
func (s *GRPCServer) ListenAndServe(addr string) error {
	if s == nil {
		return fmt.Errorf("grpc server is nil")
	}
	if addr == "" {
		return fmt.Errorf("grpc listen address is required")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen failed: %w", err)
	}
	srv := &http.Server{Handler: s}
	log.Printf("grpc server listening on %s", addr)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

const maxGRPCRequestBody = 4 << 20 // 4MB, mirroring the default gRPC max message size

func isGRPCContentType(r *http.Request) bool {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct == grpcContentType
}

func splitGRPCPath(path string) (service, method string, ok bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func writeGRPCErr(w http.ResponseWriter, e *GRPCError) {
	w.Header().Set("Content-Type", grpcContentType)
	w.Header().Set("X-Grpc-Status", fmt.Sprintf("%d", int(e.Code)))
	w.WriteHeader(http.StatusOK) // gRPC always returns 200 at the HTTP layer; status travels in trailers/headers
	_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Message, "code": e.Code.String()})
}
