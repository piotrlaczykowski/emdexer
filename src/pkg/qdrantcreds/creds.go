// Package qdrantcreds builds the gRPC transport credential option for Qdrant
// based on environment configuration.  The default (no env vars set) is
// plaintext — identical to the legacy behaviour.
package qdrantcreds

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Build returns a grpc.DialOption for Qdrant transport security.
//
// Decision table:
//
//	tlsEnabled=false           → insecure (plaintext, default)
//	tlsEnabled=true, no certs  → TLS with InsecureSkipVerify=true
//	tlsEnabled=true, caFile    → TLS with server verification via CA cert
//	all three files set        → full mTLS (client cert + CA verify)
func Build(tlsEnabled bool, certFile, keyFile, caFile string) (grpc.DialOption, error) {
	if !tlsEnabled {
		return grpc.WithTransportCredentials(insecure.NewCredentials()), nil
	}

	cfg := &tls.Config{}

	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("qdrantcreds: read CA cert %q: %w", caFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("qdrantcreds: no valid certs found in CA file %q", caFile)
		}
		cfg.RootCAs = pool
	} else {
		cfg.InsecureSkipVerify = true //nolint:gosec // opt-in for self-signed Qdrant deployments
	}

	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("qdrantcreds: load client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	return grpc.WithTransportCredentials(credentials.NewTLS(cfg)), nil
}

// FromEnv is a convenience wrapper that reads env vars and calls Build.
//
//	EMDEX_QDRANT_TLS=true       — enable TLS
//	EMDEX_QDRANT_TLS_CA=<path>  — CA cert PEM
//	EMDEX_QDRANT_TLS_CERT=<path> — client cert PEM
//	EMDEX_QDRANT_TLS_KEY=<path>  — client key PEM
func FromEnv() (grpc.DialOption, error) {
	return Build(
		os.Getenv("EMDEX_QDRANT_TLS") == "true",
		os.Getenv("EMDEX_QDRANT_TLS_CERT"),
		os.Getenv("EMDEX_QDRANT_TLS_KEY"),
		os.Getenv("EMDEX_QDRANT_TLS_CA"),
	)
}
