package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

func loadServerTLSCertificate(config Config) (tls.Certificate, string, error) {
	certPath := strings.TrimSpace(config.TLSCertPath)
	keyPath := strings.TrimSpace(config.TLSKeyPath)

	switch {
	case certPath == "" && keyPath == "":
		cert, err := generateSelfSignedTLSCertificate(config.TunnelDomain)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		return cert, "self-signed", nil
	case certPath == "" || keyPath == "":
		return tls.Certificate{}, "", fmt.Errorf("TLS_CERT_PATH and TLS_KEY_PATH must be provided together")
	default:
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, "", fmt.Errorf("loading TLS certificate failed: %w", err)
		}
		if len(cert.Certificate) > 0 {
			if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
				cert.Leaf = leaf
			}
		}
		return cert, "files", nil
	}
}

func generateSelfSignedTLSCertificate(tunnelDomain string) (tls.Certificate, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generating self-signed private key failed: %w", err)
	}

	dnsNames, ipAddresses := serverCertificateSubjects(tunnelDomain)
	commonName := "localhost"
	if len(dnsNames) > 0 {
		commonName = dnsNames[0]
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generating self-signed certificate serial failed: %w", err)
	}

	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"http-tunnels self-signed"},
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddresses,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, privateKey.Public(), privateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("creating self-signed certificate failed: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("encoding self-signed private key failed: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("loading self-signed certificate failed: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err == nil {
		cert.Leaf = leaf
	}
	return cert, nil
}

func serverCertificateSubjects(tunnelDomain string) ([]string, []net.IP) {
	domain := strings.TrimSpace(strings.ToLower(tunnelDomain))
	dnsNames := []string{"localhost"}
	ipAddresses := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}

	if domain == "localhost" {
		dnsNames = appendUniqueString(dnsNames, "*.localhost")
	} else if domain != "" {
		if ip := net.ParseIP(domain); ip != nil {
			if !containsIP(ipAddresses, ip) {
				ipAddresses = append(ipAddresses, ip)
			}
		} else {
			dnsNames = appendUniqueString(dnsNames, domain)
			if isWildcardEligibleDomain(domain) {
				dnsNames = appendUniqueString(dnsNames, "*."+domain)
			}
		}
	}

	filteredIPs := make([]net.IP, 0, len(ipAddresses))
	for _, ip := range ipAddresses {
		if ip == nil {
			continue
		}
		filteredIPs = append(filteredIPs, ip)
	}
	return dnsNames, filteredIPs
}

func isWildcardEligibleDomain(domain string) bool {
	if domain == "" || strings.Contains(domain, "*") {
		return false
	}
	if net.ParseIP(domain) != nil {
		return false
	}
	return strings.Count(domain, ".") >= 1
}

func appendUniqueString(items []string, value string) []string {
	for _, existing := range items {
		if existing == value {
			return items
		}
	}
	return append(items, value)
}

func containsIP(items []net.IP, target net.IP) bool {
	for _, existing := range items {
		if existing != nil && existing.Equal(target) {
			return true
		}
	}
	return false
}
