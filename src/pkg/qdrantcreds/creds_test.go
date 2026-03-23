package qdrantcreds_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/piotrlaczykowski/emdexer/qdrantcreds"
)

func TestQdrantTLS_InsecureDefault(t *testing.T) {
	opt, err := qdrantcreds.Build(false, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opt == nil {
		t.Fatal("expected non-nil dial option")
	}
}

func TestQdrantTLS_WithCA(t *testing.T) {
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	writeSelfSignedCA(t, caFile)

	opt, err := qdrantcreds.Build(true, "", "", caFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opt == nil {
		t.Fatal("expected non-nil dial option")
	}
}

func TestQdrantTLS_SkipVerifyWhenNoCA(t *testing.T) {
	opt, err := qdrantcreds.Build(true, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opt == nil {
		t.Fatal("expected non-nil dial option")
	}
}

func TestQdrantTLS_MissingCAFile(t *testing.T) {
	_, err := qdrantcreds.Build(true, "", "", "/nonexistent/ca.pem")
	if err == nil {
		t.Fatal("expected error for missing CA file, got nil")
	}
}

// writeSelfSignedCA generates a minimal self-signed CA certificate and writes
// it as PEM to path.
func writeSelfSignedCA(t *testing.T, path string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	defer func() { _ = f.Close() }()
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("encode pem: %v", err)
	}
}
