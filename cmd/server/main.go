package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/kaenova/http-tunnels/internal/admin"
	"github.com/kaenova/http-tunnels/internal/grpc"
	httpr "github.com/kaenova/http-tunnels/internal/http"
	itls "github.com/kaenova/http-tunnels/internal/tls"
)

func main() {
	grpcPort := getEnvInt("GRPC_PORT", 8443)
	httpPort := getEnvInt("HTTP_PORT", 8080)

	grpcAddr := fmt.Sprintf(":%d", grpcPort)
	httpAddr := fmt.Sprintf(":%d", httpPort)

	samePort := grpcPort == httpPort

	tlsCfg := itls.DefaultConfig()
	if err := itls.EnsureCertificates(tlsCfg); err != nil {
		log.Fatalf("ensuring certificates: %v", err)
	}

	serverTLS, err := itls.ServerTLSConfig(tlsCfg)
	if err != nil {
		log.Fatalf("server TLS config: %v", err)
	}

	tunnelServer := grpc.NewServer()

	// Admin handler
	adminServer := admin.NewServer(tunnelServer)
	adminMux := adminServer.Handler().(*http.ServeMux)

	// Serve admin web static files if they exist
	if _, err := os.Stat("cmd/server/web/dist"); err == nil {
		adminMux.Handle("/admin/", http.StripPrefix("/admin/", http.FileServer(http.Dir("cmd/server/web/dist"))))
	}

	if samePort {
		// Single-port mode: gRPC and HTTP share the same TLS port
		tlsListener, err := tls.Listen("tcp", grpcAddr, serverTLS)
		if err != nil {
			log.Fatalf("TLS listen: %v", err)
		}

		// Channels for routing
		grpcChan := make(chan net.Conn, 100)
		httpChan := make(chan net.Conn, 100)

		// Accept loop: detect gRPC vs HTTP and route
		go func() {
			for {
				conn, err := tlsListener.Accept()
				if err != nil {
					if isClosedError(err) {
						return
					}
					log.Printf("accept error: %v", err)
					continue
				}

				go func() {
					peeked := bufio.NewReader(conn)
					header, err := peeked.Peek(5)
					if err != nil {
						conn.Close()
						return
					}

					if string(header) == "PRI *" {
						grpcChan <- &bufferedConn{Conn: conn, reader: peeked}
					} else {
						httpChan <- &bufferedConn{Conn: conn, reader: peeked}
					}
				}()
			}
		}()

		// Start gRPC server
		go func() {
			log.Printf("gRPC server sharing port %d", grpcPort)
			grpc.StartGRPCServerOnChan(grpcChan, tunnelServer)
		}()

		// Start HTTP router
		router := httpr.NewRouter(tunnelServer, adminMux)
		go func() {
			log.Printf("HTTP router sharing port %d with gRPC", grpcPort)
			router.ServeChan(httpChan)
		}()

		log.Printf("Server started. Single port mode: gRPC+HTTP on :%d", grpcPort)
	} else {
		// Separate ports mode
		_, _, err = grpc.StartGRPCServer(grpcAddr, serverTLS, tunnelServer)
		if err != nil {
			log.Fatalf("starting gRPC server: %v", err)
		}

		httpListener, err := net.Listen("tcp", httpAddr)
		if err != nil {
			log.Fatalf("HTTP listen: %v", err)
		}

		router := httpr.NewRouter(tunnelServer, adminMux)
		go func() {
			log.Printf("HTTP router listening on %s (admin + tunnel proxy)", httpAddr)
			if err := router.Serve(httpListener); err != nil {
				log.Printf("HTTP router error: %v", err)
			}
		}()

		log.Printf("Server started. gRPC on :%d, HTTP on :%d", grpcPort, httpPort)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Printf("Shutting down...")
}

// bufferedConn wraps a net.Conn to read from a buffered reader first
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.reader.Read(p)
}

func getEnvInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}

func isClosedError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return s == "use of closed network connection" ||
		s == "connection reset" ||
		s == "broken pipe"
}

var (
	_ = fmt.Sprintf
	_ = tls.Listen
)