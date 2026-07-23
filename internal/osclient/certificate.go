package osclient

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/keymanager/v1/containers"
	"github.com/gophercloud/gophercloud/v2/openstack/keymanager/v1/secrets"
	"golang.org/x/crypto/pkcs12"
)

type listenerCertificate struct {
	Name      string
	Subject   string
	Issuer    string
	NotBefore time.Time
	NotAfter  time.Time
}

func (c *Clients) listenerCertificate(ctx context.Context, sc *serviceClients, ref string) (listenerCertificate, error) {
	if c.Filtered() {
		// A filtered global-admin selection uses the retained startup token, which
		// is not scoped to this project, so Barbican would reject the secret read.
		// Skip the doomed call and explain why rather than surfacing a raw 403.
		return listenerCertificate{}, fmt.Errorf("unavailable in filtered global-admin view (re-scope requires a role on this project)")
	}
	if sc.keyManager == nil {
		return listenerCertificate{}, fmt.Errorf("key manager service is unavailable")
	}
	kind, id, err := keyManagerReference(ref)
	if err != nil {
		return listenerCertificate{}, err
	}
	name := ""
	secretID := id
	if kind == "containers" {
		container, err := containers.Get(ctx, sc.keyManager, id).Extract()
		if err != nil {
			return listenerCertificate{}, fmt.Errorf("read certificate container: %w", err)
		}
		name = container.Name
		secretID = ""
		for _, secretRef := range container.SecretRefs {
			if strings.EqualFold(secretRef.Name, "certificate") {
				_, secretID, err = keyManagerReference(secretRef.SecretRef)
				if err != nil {
					return listenerCertificate{}, err
				}
				break
			}
		}
		if secretID == "" {
			return listenerCertificate{}, fmt.Errorf("certificate container has no certificate secret")
		}
	}
	contentType := "application/octet-stream"
	if metadata, metadataErr := secrets.Get(ctx, sc.keyManager, secretID).Extract(); metadataErr == nil {
		if name == "" {
			name = metadata.Name
		}
		if value := strings.TrimSpace(metadata.ContentTypes["default"]); value != "" {
			contentType = value
		}
	}

	payload, err := secrets.GetPayload(ctx, sc.keyManager, secretID, secrets.GetPayloadOpts{
		PayloadContentType: contentType,
	}).Extract()
	if err != nil {
		return listenerCertificate{}, fmt.Errorf("read certificate payload: %w", err)
	}
	certificate, err := parseCertificatePayload(payload)
	if err != nil {
		return listenerCertificate{}, err
	}
	return listenerCertificate{
		Name: name, Subject: certificateName(certificate.Subject.CommonName, certificate.Subject.String()),
		Issuer:    certificateName(certificate.Issuer.CommonName, certificate.Issuer.String()),
		NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
	}, nil
}

func keyManagerReference(ref string) (kind, id string, err error) {
	parsed, parseErr := url.Parse(strings.TrimSpace(ref))
	if parseErr != nil {
		return "", "", fmt.Errorf("invalid certificate reference: %w", parseErr)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := len(parts) - 2; i >= 0; i-- {
		if (parts[i] == "secrets" || parts[i] == "containers") && parts[i+1] != "" {
			return parts[i], parts[i+1], nil
		}
	}
	return "", "", fmt.Errorf("invalid certificate reference")
}

func parseCertificatePayload(payload []byte) (*x509.Certificate, error) {
	if certificate := parsePEMCertificate(payload); certificate != nil {
		return certificate, nil
	}
	blocks, err := pkcs12.ToPEM(payload, "")
	if err != nil {
		return nil, fmt.Errorf("parse certificate payload: %w", err)
	}
	for _, block := range blocks {
		if block.Type != "CERTIFICATE" {
			continue
		}
		certificate, parseErr := x509.ParseCertificate(block.Bytes)
		if parseErr == nil && !certificate.IsCA {
			return certificate, nil
		}
	}
	for _, block := range blocks {
		if block.Type == "CERTIFICATE" {
			if certificate, parseErr := x509.ParseCertificate(block.Bytes); parseErr == nil {
				return certificate, nil
			}
		}
	}
	return nil, fmt.Errorf("certificate payload contains no X.509 certificate")
}

func parsePEMCertificate(payload []byte) *x509.Certificate {
	var fallback *x509.Certificate
	for rest := payload; len(rest) > 0; {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = next
		if block.Type != "CERTIFICATE" {
			continue
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		if !certificate.IsCA {
			return certificate
		}
		if fallback == nil {
			fallback = certificate
		}
	}
	return fallback
}

func certificateName(commonName, distinguishedName string) string {
	if value := strings.TrimSpace(commonName); value != "" {
		return value
	}
	return strings.TrimSpace(distinguishedName)
}
