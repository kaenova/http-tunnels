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
	"github.com/kaenova/http-tunnels/internal/tcp"
	itls "github.com/kaenova/http-tunnels/internal/tls"
)

func main() {
	grpcPort := getEnvInt("GRPC_PORT", 8443)
	httpPort := getEnvInt("HTTP_PORT", 8080)
	adminPort := getEnvInt("ADMIN_PORT", 8090)

	grpcAddr := fmt.Sprintf(":%d", grpcPort)
	httpAddr := fmt.Sprintf(":%d", httpPort)
	adminAddr := fmt.Sprintf(":%d", adminPort)

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

	if samePort {
		// Single-port mode: accept all connections ourselves, detect gRPC vs TCP
		tlsListener, err := tls.Listen("tcp", grpcAddr, serverTLS)
		if err != nil {
			log.Fatalf("TLS listen: %v", err)
		}

		// Create a connection router
		grpcConnChan := make(chan net.Conn, 100)
		tcpConnChan := make(chan net.Conn, 100)

		// Accept loop: detect gRPC vs TCP and route
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
					// Peek at first bytes to detect gRPC
					peeked := bufio.NewReader(conn)
					header, err := peeked.Peek(5)
					if err != nil {
						conn.Close()
						return
					}

					isGRPC := string(header) == "PRI *"
					if isGRPC {
						grpcConnChan <- &bufferedConn{Conn: conn, reader: peeked}
					} else {
						tcpConnChan <- &bufferedConn{Conn: conn, reader: peeked}
					}
				}()
			}
		}()

		// Start gRPC server on the grpc connection channel
		go func() {
			log.Printf("gRPC server sharing port %d", grpcPort)
			grpc.StartGRPCServerOnChan(grpcConnChan, serverTLS, tunnelServer)
		}()

		// Start TCP forwarder on the tcp connection channel
		forwarder := tcp.NewForwarder(tunnelServer)
		go func() {
			log.Printf("TCP forwarder sharing port %d with gRPC", grpcPort)
			forwarder.ServeChan(tcpConnChan)
		}()
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

		forwarder := tcp.NewForwarder(tunnelServer)
		go func() {
			log.Printf("TCP forwarder listening on %s", httpAddr)
			if err := forwarder.Serve(httpListener); err != nil {
				log.Printf("TCP forwarder error: %v", err)
			}
		}()
	}

	// Admin API
	adminServer := admin.NewServer(tunnelServer)
	adminMux := adminServer.Handler().(*http.ServeMux)

	if _, err := os.Stat("cmd/server/web/dist"); err == nil {
		adminMux.Handle("/admin/", http.StripPrefix("/admin/", http.FileServer(http.Dir("cmd/server/web/dist"))))
	}

	go func() {
		log.Printf("Admin API listening on %s", adminAddr)
		if err := http.ListenAndServe(adminAddr, adminMux); err != nil {
			log.Printf("Admin server error: %v", err)
		}
	}()

	if samePort {
		log.Printf("Server started. Single port mode: gRPC+TCP on :%d, Admin on :%d", grpcPort, adminPort)
	} else {
		log.Printf("Server started. gRPC on :%d, TCP on :%d, Admin on :%d", grpcPort, httpPort, adminPort)
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