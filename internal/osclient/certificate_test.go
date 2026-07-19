package osclient

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestKeyManagerReference(t *testing.T) {
	for _, test := range []struct {
		ref      string
		wantKind string
		wantID   string
	}{
		{"https://barbican.example/v1/secrets/secret-id", "secrets", "secret-id"},
		{"https://barbican.example/v1/containers/container-id", "containers", "container-id"},
	} {
		kind, id, err := keyManagerReference(test.ref)
		if err != nil || kind != test.wantKind || id != test.wantID {
			t.Errorf("keyManagerReference(%q) = %q, %q, %v", test.ref, kind, id, err)
		}
	}
	if _, _, err := keyManagerReference("not-a-key-manager-reference"); err == nil {
		t.Fatal("invalid reference was accepted")
	}
}

func TestParseCertificatePayloadPEM(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "api.example.test"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(90 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	payload := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certificate, err := parseCertificatePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if certificate.Subject.CommonName != "api.example.test" || !certificate.NotAfter.Equal(template.NotAfter) {
		t.Fatalf("unexpected certificate: subject=%q expires=%v", certificate.Subject.CommonName, certificate.NotAfter)
	}
}
