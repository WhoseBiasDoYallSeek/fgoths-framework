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
	"context"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"
)

// ClientIdentity describes the identity presented by a client via its mTLS certificate.
type ClientIdentity struct {
	CommonName string
	DNSNames   []string
	URIs       []string
	Cert       *x509.Certificate
}

var identityContextKey = struct{}{}

// ContextClientIdentity returns the mTLS client identity validated for this request, if any.
func ContextClientIdentity(r *http.Request) (*ClientIdentity, bool) {
	if r == nil {
		return nil, false
	}
	identity, ok := r.Context().Value(identityContextKey).(*ClientIdentity)
	return identity, ok
}

func clientIdentityFromRequest(r *http.Request) *ClientIdentity {
	if r == nil || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return nil
	}
	cert := r.TLS.PeerCertificates[0]
	uris := make([]string, 0, len(cert.URIs))
	for _, u := range cert.URIs {
		uris = append(uris, u.String())
	}
	return &ClientIdentity{
		CommonName: cert.Subject.CommonName,
		DNSNames:   append([]string(nil), cert.DNSNames...),
		URIs:       uris,
		Cert:       cert,
	}
}

// IdentityPolicy restricts a route to specific mTLS client identities.
// A route protected by IdentityPolicy requires a client certificate to have
// already been verified by the server's TLS configuration (RequireAndVerifyClientCert);
// this middleware only matches the verified identity against the allow-list.
type IdentityPolicy struct {
	// AllowedCommonNames restricts access to client certificates with one of these CNs.
	AllowedCommonNames []string
	// AllowedDNSNames restricts access to client certificates presenting one of these SAN DNS names.
	AllowedDNSNames []string
	// AllowedURIs restricts access to client certificates presenting one of these SAN URIs
	// (useful for SPIFFE-style service identities such as spiffe://cluster/ns/svc).
	AllowedURIs []string
}

func (p IdentityPolicy) isEmpty() bool {
	return len(p.AllowedCommonNames) == 0 && len(p.AllowedDNSNames) == 0 && len(p.AllowedURIs) == 0
}

func (p IdentityPolicy) allows(identity *ClientIdentity) bool {
	if p.isEmpty() {
		return true
	}
	if identity == nil {
		return false
	}
	if len(p.AllowedCommonNames) > 0 && containsFold(p.AllowedCommonNames, identity.CommonName) {
		return true
	}
	if len(p.AllowedDNSNames) > 0 {
		for _, name := range identity.DNSNames {
			if containsFold(p.AllowedDNSNames, name) {
				return true
			}
		}
	}
	if len(p.AllowedURIs) > 0 {
		for _, uri := range identity.URIs {
			if containsFold(p.AllowedURIs, uri) {
				return true
			}
		}
	}
	return false
}

func containsFold(values []string, want string) bool {
	if want == "" {
		return false
	}
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

// RequireClientIdentity enforces that the request presented a verified mTLS client
// certificate matching the given identity policy, and exposes the identity to
// downstream handlers via ContextClientIdentity. It must run behind a server
// configured with WithMutualTLS (or an equivalent tls.Config with
// RequireAndVerifyClientCert), otherwise no certificate will ever be present
// and every request will be rejected.
func RequireClientIdentity(policy IdentityPolicy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := clientIdentityFromRequest(r)
			if identity == nil {
				http.Error(w, "client certificate required", http.StatusUnauthorized)
				return
			}
			if !policy.allows(identity) {
				http.Error(w, fmt.Sprintf("identity %q is not authorized for this route", identity.CommonName), http.StatusForbidden)
				return
			}
			ctx := context.WithValue(r.Context(), identityContextKey, identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
