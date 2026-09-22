package grpcapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"{{.ProjectName}}/pkg/runtime"
)

// Service is an example gRPC-style service generated with the grpc feature.
// Replace the methods with your real business calls; the wire contract stays
// the same: POST /<service>/<method> with JSON bodies and x-grpc-status codes.
type Service struct{}

// NewService registers the example service on the given gRPC server.
func NewServer() (*runtime.GRPCServer, error) {
	srv := runtime.NewGRPCServer()
	svc := &runtime.GRPCService{
		Name: "{{.ProjectName}}.v1.Example",
		Methods: map[string]runtime.GRPCMethod{
			"Ping": ping,
		},
	}
	if err := srv.Register(svc); err != nil {
		return nil, err
	}
	return srv, nil
}

// ping is a unary method demonstrating request/response handling and error mapping.
func ping(ctx context.Context, req json.RawMessage) (any, *runtime.GRPCError) {
	var in struct {
		Message string `json:"message"`
	}
	if len(req) > 0 {
		if err := json.Unmarshal(req, &in); err != nil {
			return nil, &runtime.GRPCError{Code: runtime.GRPCInvalidArgument, Message: "invalid JSON body"}
		}
	}
	if in.Message == "" {
		return nil, &runtime.GRPCError{Code: runtime.GRPCInvalidArgument, Message: "message is required"}
	}
	return map[string]string{"reply": fmt.Sprintf("pong: %s", in.Message)}, nil
}

// HealthHandler exposes the gRPC health endpoint for the HTTP mux, mapped to
// the conventional /health/grpc path.
func HealthHandler(srv *runtime.GRPCServer) http.Handler {
	return srv.HealthHTTP()
}
