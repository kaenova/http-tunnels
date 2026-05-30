package tls

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
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	CertFile string
	KeyFile  string
}

func DefaultConfig() Config {
	certDir := os.Getenv("TLS_CERT_DIR")
	if certDir == "" {
		certDir = "tls"
	}
	return Config{
		CertFile: filepath.Join(certDir, "server.crt"),
		KeyFile:  filepath.Join(certDir, "server.key"),
	}
}

func EnsureCertificates(cfg Config) error {
	// Check if custom cert files are provided via env
	if certEnv := os.Getenv("TLS_CERT_FILE"); certEnv != "" {
		cfg.CertFile = certEnv
	}
	if keyEnv := os.Getenv("TLS_KEY_FILE"); keyEnv != "" {
		cfg.KeyFile = keyEnv
	}

	// Check if files exist
	if _, err := os.Stat(cfg.CertFile); err == nil {
		if _, err := os.Stat(cfg.KeyFile); err == nil {
			return nil
		}
	}

	// Generate self-signed cert
	dir := filepath.Dir(cfg.CertFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating cert dir: %w", err)
	}

	fmt.Printf("Generating self-signed TLS certificates to %s, %s\n", cfg.CertFile, cfg.KeyFile)

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generating key: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			Organization: []string{"HTTP Tunnels Self-Signed"},
			CommonName:   "tunnel.local",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost", "tunnel.local"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("creating cert: %w", err)
	}

	if err := os.WriteFile(cfg.CertFile, certDER, 0644); err != nil {
		return fmt.Errorf("writing cert: %w", err)
	}

	// Write cert as PEM
	certFile, err := os.Create(cfg.CertFile)
	if err != nil {
		return fmt.Errorf("creating cert file: %w", err)
	}
	defer certFile.Close()
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return fmt.Errorf("encoding cert: %w", err)
	}

	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}

	keyFile, err := os.Create(cfg.KeyFile)
	if err != nil {
		return fmt.Errorf("creating key file: %w", err)
	}
	defer keyFile.Close()
	if err := pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes}); err != nil {
		return fmt.Errorf("encoding key: %w", err)
	}

	fmt.Println("Self-signed TLS certificates generated")
	return nil
}

func ServerTLSConfig(cfg Config) (*tls.Config, error) {
	if certEnv := os.Getenv("TLS_CERT_FILE"); certEnv != "" {
		cfg.CertFile = certEnv
	}
	if keyEnv := os.Getenv("TLS_KEY_FILE"); keyEnv != "" {
		cfg.KeyFile = keyEnv
	}

	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("loading server TLS cert: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
	}, nil
}

func ClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
		NextProtos:         []string{"h2"},
	}
}