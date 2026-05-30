package tunnel

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Default paths for self-signed certificates
const (
	defaultCertDir  = "/data/tls"
	defaultCertFile = "server.crt"
	defaultKeyFile  = "server.key"
)

// TLSConfig holds paths for TLS certificates
type TLSConfig struct {
	CertFile string
	KeyFile  string
	CertDir  string
}

// DefaultTLSConfig returns TLS config with environment variable overrides
func DefaultTLSConfig() TLSConfig {
	cfg := TLSConfig{
		CertDir: getEnv("TLS_CERT_DIR", defaultCertDir),
		CertFile: getEnv("TLS_CERT_FILE", ""),
		KeyFile:  getEnv("TLS_KEY_FILE", ""),
	}

	// If individual files not set, use dir + default names
	if cfg.CertFile == "" {
		cfg.CertFile = filepath.Join(cfg.CertDir, defaultCertFile)
	}
	if cfg.KeyFile == "" {
		cfg.KeyFile = filepath.Join(cfg.CertDir, defaultKeyFile)
	}

	return cfg
}

// EnsureCertificates checks if cert files exist, generates self-signed if not
func EnsureCertificates(cfg TLSConfig) error {
	// Check if both files exist
	if fileExists(cfg.CertFile) && fileExists(cfg.KeyFile) {
		log.Printf("TLS certificates found at %s, %s", cfg.CertFile, cfg.KeyFile)
		return nil
	}

	// Generate self-signed
	log.Printf("Generating self-signed TLS certificates to %s, %s", cfg.CertFile, cfg.KeyFile)

	// Ensure directory exists
	dir := filepath.Dir(cfg.CertFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating cert directory: %w", err)
	}

	// Generate self-signed certificate
	cert, key, err := generateSelfSignedCert("tunnel.local", 365 * 24 * time.Hour)
	if err != nil {
		return fmt.Errorf("generating self-signed cert: %w", err)
	}

	if err := os.WriteFile(cfg.CertFile, cert, 0644); err != nil {
		return fmt.Errorf("writing cert file: %w", err)
	}
	if err := os.WriteFile(cfg.KeyFile, key, 0600); err != nil {
		return fmt.Errorf("writing key file: %w", err)
	}

	log.Printf("Self-signed TLS certificates generated")
	return nil
}

// ServerTLSConfig creates a *tls.Config for server use
func ServerTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("loading server TLS cert: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2"}, // Advertise HTTP/2
	}, nil
}

// ClientTLSConfig creates a *tls.Config for client use (skip verify for self-signed)
func ClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true, // Self-signed cert
		MinVersion:         tls.VersionTLS12,
		NextProtos:         []string{"h2"},
	}
}

// ListenTLS creates a TLS listener
func ListenTLS(addr string, tlsCfg *tls.Config) (net.Listener, error) {
	return tls.Listen("tcp", addr, tlsCfg)
}

// HTTPSServer creates an HTTPS server with TLS
func HTTPSServer(addr string, handler http.Handler, tlsCfg *tls.Config) *http.Server {
	return &http.Server{
		Addr:      addr,
		Handler:   handler,
		TLSConfig: tlsCfg,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}