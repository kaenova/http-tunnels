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
	// Listen address (single port for both tunnel + HTTP)
	listenAddr := ":8443"
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		listenAddr = v
	}

	// TLS config
	tlsCfg := tunnel.DefaultTLSConfig()
	if err := tunnel.EnsureCertificates(tlsCfg); err != nil {
		log.Fatalf("ensuring certificates: %v", err)
	}

	serverTLS, err := tunnel.ServerTLSConfig(tlsCfg)
	if err != nil {
		log.Fatalf("server TLS config: %v", err)
	}

	// TLS listener
	listener, err := tunnel.ListenTLS(listenAddr, serverTLS)
	if err != nil {
		log.Fatalf("listening: %v", err)
	}
	log.Printf("Server listening on %s", listenAddr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Channel to receive tunnel sessions
	sessions := make(chan *tunnel.Session, 10)

	// Mux: detect whether incoming connection is HTTP/2 tunnel or regular HTTP
	mux := newSessionMux(listener, sessions)

	// Accept connections
	go func() {
		if err := mux.serve(ctx); err != nil {
			log.Printf("serve error: %v", err)
		}
	}()

	// HTTP server that uses the tunnel session
	httpServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case session := <-sessions:
				session.HTTPHandler().ServeHTTP(w, r)
				select {
				case sessions <- session:
				default:
				}
			default:
				http.Error(w, "No tunnel client connected", http.StatusServiceUnavailable)
			}
		}),
	}

	// Start HTTP on the same listener (after TLS)
	go func() {
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Printf("Shutting down...")
	cancel()
	httpServer.Shutdown(context.Background())
}

// sessionMux peeks at the first bytes of a TLS connection to determine
// if it's an HTTP/2 tunnel client or a regular HTTP request
type sessionMux struct {
	listener net.Listener
	sessions chan<- *tunnel.Session
}

func newSessionMux(l net.Listener, s chan<- *tunnel.Session) *sessionMux {
	return &sessionMux{listener: l, sessions: s}
}

func (m *sessionMux) serve(ctx context.Context) error {
	for {
		conn, err := m.listener.Accept()
		if err != nil {
			if isClosedError(err) {
				return nil
			}
			log.Printf("accept error: %v", err)
			continue
		}

		// All connections are TLS, we need to detect if it's a tunnel client
		// by attempting to read the HTTP/2 client preface (PRI * HTTP/2.0)
		// or by attempting an HTTP/2 handshake
		go func() {
			// Try to create a server session (will fail if it's a browser)
			session, err := tunnel.NewServerSession(conn)
			if err != nil {
				// Not a tunnel client — close and let HTTP server handle it
				conn.Close()
				return
			}
			log.Printf("New tunnel session established")
			select {
			case m.sessions <- session:
			default:
				log.Printf("Session channel full, closing session")
				session.Close()
			}
		}()
	}
}

func isClosedError(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() == "use of closed network connection" ||
		err.Error() == "connection reset" ||
		err.Error() == "broken pipe"
}

var _ = net.Listen