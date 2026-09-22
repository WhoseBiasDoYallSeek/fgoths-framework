// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package runtime

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
)

// TLSConfig configures server-side and upstream TLS behavior for the FGOTHS runtime.
type TLSConfig struct {
	CertFile       string
	KeyFile        string
	ClientCAFile   string
	RequireClient  bool
	MinVersion     uint16
	ServerName     string
	ClientCertFile string
	ClientKeyFile  string
	RootCAFile     string
}

// WithMutualTLS enables TLS with optional mTLS verification on the server.
func (s *Server) WithMutualTLS(certFile, keyFile, clientCAFile string) error {
	if s == nil {
		return fmt.Errorf("server is nil")
	}
	cfg, err := BuildServerTLSConfig(certFile, keyFile, clientCAFile)
	if err != nil {
		return err
	}
	s.Server.TLSConfig = cfg
	return nil
}

// BuildServerTLSConfig returns a TLS configuration that can enforce client certificate validation.
func BuildServerTLSConfig(certFile, keyFile, clientCAFile string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("certificate and key file are required")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	if clientCAFile != "" {
		caPEM, err := os.ReadFile(clientCAFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("failed to parse client CA certificate")
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}

// WithUpstreamTLS configures the proxy transport to use mTLS credentials when calling upstream services.
// It returns an error instead of silently skipping misconfigured credentials.
func (p *Proxy) WithUpstreamTLS(certFile, keyFile, caFile string) (*Proxy, error) {
	if p == nil {
		return nil, fmt.Errorf("proxy is nil")
	}
	if certFile == "" && keyFile == "" && caFile == "" {
		return p, nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return nil, fmt.Errorf("certFile and keyFile must both be set")
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load upstream client certificate: %w", err)
		}
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.Certificates = []tls.Certificate{cert}
	}
	if caFile != "" {
		caPEM, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read upstream CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("failed to parse upstream CA certificate")
		}
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	if transport.TLSClientConfig != nil {
		transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	p.reverseProxy.Transport = transport
	return p, nil
}
