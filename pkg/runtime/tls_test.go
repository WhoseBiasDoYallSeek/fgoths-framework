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
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerMutualTLS(t *testing.T) {
	dir := t.TempDir()
	caCertPath, caKeyPath := writeCertificateAuthority(t, dir, "fgoths-ca")
	serverCertPath, serverKeyPath := writeSignedCert(t, dir, "localhost", caCertPath, caKeyPath, true)
	clientCertPath, clientKeyPath := writeSignedCert(t, dir, "client-app", caCertPath, caKeyPath, false)

	server := NewServer("127.0.0.1:0")
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			t.Fatalf("expected client certificate in request")
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("mtls-ok"))
	})
	if err := server.WithMutualTLS(serverCertPath, serverKeyPath, caCertPath); err != nil {
		t.Fatalf("WithMutualTLS failed: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()

	go func() {
		_ = server.ServeTLS(ln, "", "")
	}()
	defer server.Close()

	serverURL := "https://" + ln.Addr().String()
	clientCert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		t.Fatalf("load client cert: %v", err)
	}
	rootPool := x509.NewCertPool()
	caPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		t.Fatalf("read ca pem: %v", err)
	}
	rootPool.AppendCertsFromPEM(caPEM)

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:      rootPool,
		Certificates: []tls.Certificate{clientCert},
		MinVersion:   tls.VersionTLS12,
	}}}
	res, err := client.Get(serverURL)
	if err != nil {
		t.Fatalf("client request failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.StatusCode)
	}

	badClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: rootPool, MinVersion: tls.VersionTLS12}}}
	_, err = badClient.Get(serverURL)
	if err == nil {
		t.Fatal("expected request without client certificate to fail")
	}
}

func TestProxyWithUpstreamTLSAppliesCredentials(t *testing.T) {
	dir := t.TempDir()
	caCertPath, caKeyPath := writeCertificateAuthority(t, dir, "fgoths-ca")
	clientCertPath, clientKeyPath := writeSignedCert(t, dir, "client-app", caCertPath, caKeyPath, false)

	proxy, err := NewProxy("https://upstream.internal")
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	if _, err := proxy.WithUpstreamTLS(clientCertPath, clientKeyPath, caCertPath); err != nil {
		t.Fatalf("WithUpstreamTLS failed: %v", err)
	}
}

func TestProxyWithUpstreamTLSFailsClosedOnBadCert(t *testing.T) {
	dir := t.TempDir()
	badCertPath := filepath.Join(dir, "bad-cert.pem")
	badKeyPath := filepath.Join(dir, "bad-key.pem")
	if err := os.WriteFile(badCertPath, []byte("not a cert"), 0o600); err != nil {
		t.Fatalf("write bad cert: %v", err)
	}
	if err := os.WriteFile(badKeyPath, []byte("not a key"), 0o600); err != nil {
		t.Fatalf("write bad key: %v", err)
	}

	proxy, err := NewProxy("https://upstream.internal")
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	if _, err := proxy.WithUpstreamTLS(badCertPath, badKeyPath, ""); err == nil {
		t.Fatal("expected WithUpstreamTLS to fail closed on an invalid client certificate")
	}
}

func TestProxyWithUpstreamTLSFailsClosedOnBadCA(t *testing.T) {
	dir := t.TempDir()
	badCAPath := filepath.Join(dir, "bad-ca.pem")
	if err := os.WriteFile(badCAPath, []byte("not a ca cert"), 0o600); err != nil {
		t.Fatalf("write bad ca: %v", err)
	}

	proxy, err := NewProxy("https://upstream.internal")
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	if _, err := proxy.WithUpstreamTLS("", "", badCAPath); err == nil {
		t.Fatal("expected WithUpstreamTLS to fail closed on an invalid CA certificate")
	}
}

func TestProxyWithUpstreamTLSFailsClosedOnIncompletePair(t *testing.T) {
	dir := t.TempDir()
	caCertPath, caKeyPath := writeCertificateAuthority(t, dir, "fgoths-ca")
	clientCertPath, _ := writeSignedCert(t, dir, "client-app", caCertPath, caKeyPath, false)

	proxy, err := NewProxy("https://upstream.internal")
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	if _, err := proxy.WithUpstreamTLS(clientCertPath, "", ""); err == nil {
		t.Fatal("expected WithUpstreamTLS to fail closed when only certFile is set without keyFile")
	}
}

func TestBuildServerTLSConfigErrors(t *testing.T) {
	// Empty cert/key paths.
	if _, err := BuildServerTLSConfig("", "key.pem", ""); err == nil {
		t.Fatal("expected error for empty certFile")
	}
	if _, err := BuildServerTLSConfig("cert.pem", "", ""); err == nil {
		t.Fatal("expected error for empty keyFile")
	}

	// Non-existent cert/key files.
	if _, err := BuildServerTLSConfig("/nonexistent/cert.pem", "/nonexistent/key.pem", ""); err == nil {
		t.Fatal("expected error for non-existent cert")
	}

	// Valid cert/key but non-existent CA.
	dir := t.TempDir()
	caCertPath, caKeyPath := writeCertificateAuthority(t, dir, "fgoths-ca")
	serverCertPath, serverKeyPath := writeSignedCert(t, dir, "localhost", caCertPath, caKeyPath, true)
	if _, err := BuildServerTLSConfig(serverCertPath, serverKeyPath, "/nonexistent/ca.pem"); err == nil {
		t.Fatal("expected error for non-existent CA file")
	}

	// Valid cert/key but invalid CA content.
	badCAPath := filepath.Join(dir, "bad-ca.pem")
	if err := os.WriteFile(badCAPath, []byte("not a ca cert"), 0o600); err != nil {
		t.Fatalf("write bad ca: %v", err)
	}
	if _, err := BuildServerTLSConfig(serverCertPath, serverKeyPath, badCAPath); err == nil {
		t.Fatal("expected error for invalid CA content")
	}

	// Valid cert/key with valid CA (success path).
	if cfg, err := BuildServerTLSConfig(serverCertPath, serverKeyPath, caCertPath); err != nil {
		t.Fatalf("BuildServerTLSConfig with valid CA: %v", err)
	} else if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestWithMutualTLSNilServer(t *testing.T) {
	var s *Server
	if err := s.WithMutualTLS("cert.pem", "key.pem", "ca.pem"); err == nil {
		t.Fatal("expected error for nil server")
	}
}

func TestProxyWithUpstreamTLSNilProxy(t *testing.T) {
	var p *Proxy
	_, err := p.WithUpstreamTLS("cert.pem", "key.pem", "ca.pem")
	if err == nil {
		t.Fatal("expected error for nil proxy")
	}
}

func TestProxyWithUpstreamTLSNoCreds(t *testing.T) {
	proxy, err := NewProxy("https://upstream.internal")
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	// Empty credentials = no-op, should return the proxy unchanged.
	if _, err := proxy.WithUpstreamTLS("", "", ""); err != nil {
		t.Fatalf("WithUpstreamTLS with no credentials: %v", err)
	}
}

func writeCertificateAuthority(t *testing.T, dir, commonName string) (certPath, keyPath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	certTmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, certTmpl, certTmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create ca cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPath = filepath.Join(dir, commonName+".pem")
	keyPath = filepath.Join(dir, commonName+".key")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write ca cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write ca key: %v", err)
	}
	return certPath, keyPath
}

func writeSignedCert(t *testing.T, dir, commonName, caCertPath, caKeyPath string, isServer bool) (certPath, keyPath string) {
	t.Helper()
	caCertPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		t.Fatalf("read ca cert: %v", err)
	}
	caKeyPEM, err := os.ReadFile(caKeyPath)
	if err != nil {
		t.Fatalf("read ca key: %v", err)
	}
	caBlock, _ := pem.Decode(caCertPEM)
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("parse ca cert: %v", err)
	}
	caKeyBlock, _ := pem.Decode(caKeyPEM)
	caKey, err := x509.ParsePKCS1PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		t.Fatalf("parse ca key: %v", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	if !isServer {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPath = filepath.Join(dir, commonName+".pem")
	keyPath = filepath.Join(dir, commonName+".key")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write cert key: %v", err)
	}
	return certPath, keyPath
}
