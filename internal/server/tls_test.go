package server

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"
)

func TestGenerateSelfSignedTLSCertificateIncludesTunnelSubjects(t *testing.T) {
	cert, err := generateSelfSignedTLSCertificate("t.example.test")
	if err != nil {
		t.Fatalf("generate self-signed certificate: %v", err)
	}
	leaf := certificateLeaf(t, cert)

	assertContainsString(t, leaf.DNSNames, "t.example.test")
	assertContainsString(t, leaf.DNSNames, "*.t.example.test")
	assertContainsString(t, leaf.DNSNames, "localhost")
	assertContainsIP(t, leaf.IPAddresses, net.ParseIP("127.0.0.1"))
}

func TestGenerateSelfSignedTLSCertificateForLocalhostIncludesWildcard(t *testing.T) {
	cert, err := generateSelfSignedTLSCertificate("localhost")
	if err != nil {
		t.Fatalf("generate self-signed certificate: %v", err)
	}
	leaf := certificateLeaf(t, cert)

	assertContainsString(t, leaf.DNSNames, "localhost")
	assertContainsString(t, leaf.DNSNames, "*.localhost")
}

func certificateLeaf(t *testing.T, cert tls.Certificate) *x509.Certificate {
	t.Helper()
	if cert.Leaf != nil {
		return cert.Leaf
	}
	if len(cert.Certificate) == 0 {
		t.Fatalf("certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse certificate leaf: %v", err)
	}
	return leaf
}

func assertContainsString(t *testing.T, items []string, target string) {
	t.Helper()
	for _, item := range items {
		if item == target {
			return
		}
	}
	t.Fatalf("expected %q in %v", target, items)
}

func assertContainsIP(t *testing.T, items []net.IP, target net.IP) {
	t.Helper()
	for _, item := range items {
		if item.Equal(target) {
			return
		}
	}
	t.Fatalf("expected IP %v in %v", target, items)
}
