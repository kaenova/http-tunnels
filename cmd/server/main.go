package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/kaenova/http-tunnels/internal/tunnel"
)

func main() {
	// TLS config
	tlsCfg := tunnel.DefaultTLSConfig()
	if err := tunnel.EnsureCertificates(tlsCfg); err != nil {
		log.Fatalf("ensuring certificates: %v", err)
	}

	serverTLS, err := tunnel.ServerTLSConfig(tlsCfg)
	if err != nil {
		log.Fatalf("server TLS config: %v", err)
	}

	// Listen for tunnel connections from clients
	tunnelAddr := getEnv("TUNNEL_LISTEN_ADDR", ":50051")
	tunnelListener, err := tunnel.ListenTLS(tunnelAddr, serverTLS)
	if err != nil {
		log.Fatalf("listening for tunnel connections: %v", err)
	}
	log.Printf("Tunnel server listening on %s", tunnelAddr)

	// HTTP server for browser requests (proxied by NPM)
	httpAddr := getEnv("HTTP_LISTEN_ADDR", ":8080")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Channel to receive tunnel sessions
	sessions := make(chan *tunnel.Session, 10)

	// Accept tunnel sessions from clients
	go func() {
		if err := tunnel.ServeTunnel(ctx, tunnelListener, func(session *tunnel.Session) {
			log.Printf("New tunnel session established")
			select {
			case sessions <- session:
			default:
				log.Printf("Session channel full, closing session")
				session.Close()
			}
		}); err != nil {
			log.Printf("tunnel server error: %v", err)
		}
	}()

	// HTTP server that uses the tunnel session
	httpServer := &http.Server{
		Addr: httpAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get the first available session
			select {
			case session := <-sessions:
				// Create HTTP handler from session and serve
				session.HTTPHandler().ServeHTTP(w, r)
				// Put session back
				select {
				case sessions <- session:
				default:
					log.Printf("Cannot return session to pool")
				}
			default:
				http.Error(w, "No tunnel client connected", http.StatusServiceUnavailable)
			}
		}),
	}

	go func() {
		log.Printf("HTTP server listening on %s", httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Wait for signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Printf("Shutting down...")
	cancel()
	httpServer.Shutdown(context.Background())
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Ensure net is used
var _ = net.Listen