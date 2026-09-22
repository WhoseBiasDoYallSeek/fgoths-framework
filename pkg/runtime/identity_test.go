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
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestRequireClientIdentityAllowsMatchingCommonName(t *testing.T) {
	dir := t.TempDir()
	caCertPath, caKeyPath := writeCertificateAuthority(t, dir, "fgoths-ca")
	serverCertPath, serverKeyPath := writeSignedCert(t, dir, "localhost", caCertPath, caKeyPath, true)
	clientCertPath, clientKeyPath := writeSignedCert(t, dir, "billing-service", caCertPath, caKeyPath, false)

	server := NewServer("127.0.0.1:0")
	mw := RequireClientIdentity(IdentityPolicy{AllowedCommonNames: []string{"billing-service"}})
	server.Handler = mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := ContextClientIdentity(r)
		if !ok || identity.CommonName != "billing-service" {
			t.Fatalf("expected identity billing-service, got %+v (ok=%v)", identity, ok)
		}
		w.WriteHeader(http.StatusOK)
	}))
	if err := server.WithMutualTLS(serverCertPath, serverKeyPath, caCertPath); err != nil {
		t.Fatalf("WithMutualTLS failed: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()
	go func() { _ = server.ServeTLS(ln, "", "") }()
	defer server.Close()

	client := newMTLSClient(t, caCertPath, clientCertPath, clientKeyPath)
	res, err := client.Get("https://" + ln.Addr().String())
	if err != nil {
		t.Fatalf("client request failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
}

func TestRequireClientIdentityRejectsUnauthorizedCommonName(t *testing.T) {
	dir := t.TempDir()
	caCertPath, caKeyPath := writeCertificateAuthority(t, dir, "fgoths-ca")
	serverCertPath, serverKeyPath := writeSignedCert(t, dir, "localhost", caCertPath, caKeyPath, true)
	clientCertPath, clientKeyPath := writeSignedCert(t, dir, "untrusted-service", caCertPath, caKeyPath, false)

	server := NewServer("127.0.0.1:0")
	mw := RequireClientIdentity(IdentityPolicy{AllowedCommonNames: []string{"billing-service"}})
	server.Handler = mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if err := server.WithMutualTLS(serverCertPath, serverKeyPath, caCertPath); err != nil {
		t.Fatalf("WithMutualTLS failed: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()
	go func() { _ = server.ServeTLS(ln, "", "") }()
	defer server.Close()

	client := newMTLSClient(t, caCertPath, clientCertPath, clientKeyPath)
	res, err := client.Get("https://" + ln.Addr().String())
	if err != nil {
		t.Fatalf("client request failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", res.StatusCode)
	}
}

func TestIdentityPolicyAllowsBranches(t *testing.T) {
	identity := &ClientIdentity{
		CommonName: "billing-service",
		DNSNames:   []string{"billing.internal"},
		URIs:       []string{"spiffe://cluster/ns/prod/sa/billing"},
	}

	t.Run("empty policy allows everything", func(t *testing.T) {
		if !(IdentityPolicy{}).allows(identity) {
			t.Fatal("expected an empty policy to allow any verified identity")
		}
	})
	t.Run("nil identity is rejected by a non-empty policy", func(t *testing.T) {
		if (IdentityPolicy{AllowedCommonNames: []string{"x"}}).allows(nil) {
			t.Fatal("expected a nil identity to be rejected by a non-empty policy")
		}
	})
	t.Run("common name match", func(t *testing.T) {
		if !(IdentityPolicy{AllowedCommonNames: []string{"Billing-Service"}}).allows(identity) {
			t.Fatal("expected a case-insensitive CN match to be allowed")
		}
	})
	t.Run("dns name match", func(t *testing.T) {
		if !(IdentityPolicy{AllowedDNSNames: []string{"billing.internal"}}).allows(identity) {
			t.Fatal("expected a SAN DNS match to be allowed")
		}
	})
	t.Run("uri match", func(t *testing.T) {
		if !(IdentityPolicy{AllowedURIs: []string{"spiffe://cluster/ns/prod/sa/billing"}}).allows(identity) {
			t.Fatal("expected a SAN URI match to be allowed")
		}
	})
	t.Run("no match", func(t *testing.T) {
		if (IdentityPolicy{AllowedCommonNames: []string{"other"}}).allows(identity) {
			t.Fatal("expected an unmatched policy to reject the identity")
		}
	})
}

func TestIdentityPolicyHelpers(t *testing.T) {
	if !(IdentityPolicy{}).isEmpty() {
		t.Fatal("expected an empty policy to report isEmpty")
	}
	if (IdentityPolicy{AllowedCommonNames: []string{"x"}}).isEmpty() {
		t.Fatal("expected a non-empty policy to report not isEmpty")
	}
	if containsFold([]string{"a"}, "") {
		t.Fatal("expected an empty want to never match")
	}
	if !containsFold([]string{"ABC"}, "abc") {
		t.Fatal("expected containsFold to be case-insensitive")
	}
}

func TestContextClientIdentityNilSafety(t *testing.T) {
	if _, ok := ContextClientIdentity(nil); ok {
		t.Fatal("expected ContextClientIdentity on a nil request to report not found")
	}
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if _, ok := ContextClientIdentity(req); ok {
		t.Fatal("expected ContextClientIdentity without middleware to report not found")
	}
	if got := clientIdentityFromRequest(req); got != nil {
		t.Fatalf("expected clientIdentityFromRequest to return nil without TLS, got %#v", got)
	}
	if got := clientIdentityFromRequest(nil); got != nil {
		t.Fatalf("expected clientIdentityFromRequest(nil) to return nil, got %#v", got)
	}
}

func newMTLSClient(t *testing.T, caCertPath, clientCertPath, clientKeyPath string) *http.Client {
	t.Helper()
	clientCert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		t.Fatalf("load client cert: %v", err)
	}
	caPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		t.Fatalf("read ca pem: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatalf("failed to parse ca certificate")
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{clientCert},
		MinVersion:   tls.VersionTLS12,
	}}}
}
