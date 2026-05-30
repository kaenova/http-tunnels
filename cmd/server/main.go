package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/kaenova/http-tunnels/internal/admin"
	"github.com/kaenova/http-tunnels/internal/grpc"
	"github.com/kaenova/http-tunnels/internal/tcp"
	itls "github.com/kaenova/http-tunnels/internal/tls"
)

func main() {
	listenAddr := ":8443"
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		listenAddr = v
	}

	// TLS config
	tlsCfg := itls.DefaultConfig()
	if err := itls.EnsureCertificates(tlsCfg); err != nil {
		log.Fatalf("ensuring certificates: %v", err)
	}

	serverTLS, err := itls.ServerTLSConfig(tlsCfg)
	if err != nil {
		log.Fatalf("server TLS config: %v", err)
	}

	// Create tunnel server
	tunnelServer := grpc.NewServer()

	// Start gRPC server
	_, _, err = grpc.StartGRPCServer(listenAddr, serverTLS, tunnelServer)
	if err != nil {
		log.Fatalf("starting gRPC server: %v", err)
	}

	// TCP forwarder port (for non-gRPC traffic)
	tcpAddr := ":8080"
	if v := os.Getenv("TCP_LISTEN_ADDR"); v != "" {
		tcpAddr = v
	}

	tcpListener, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		log.Fatalf("TCP listen: %v", err)
	}

	forwarder := tcp.NewForwarder(tunnelServer)
	go func() {
		log.Printf("TCP forwarder listening on %s", tcpAddr)
		if err := forwarder.Serve(tcpListener); err != nil {
			log.Printf("TCP forwarder error: %v", err)
		}
	}()

	// Admin API
	adminServer := admin.NewServer(tunnelServer)
	adminMux := adminServer.Handler().(*http.ServeMux)

	// Also serve admin web static files if they exist
	adminMux.Handle("/admin/", http.StripPrefix("/admin/", http.FileServer(http.Dir("cmd/server/web/dist"))))

	// Start admin HTTP server on a separate port
	adminAddr := ":8090"
	if v := os.Getenv("ADMIN_LISTEN_ADDR"); v != "" {
		adminAddr = v
	}

	go func() {
		log.Printf("Admin API listening on %s", adminAddr)
		if err := http.ListenAndServe(adminAddr, adminMux); err != nil {
			log.Printf("Admin server error: %v", err)
		}
	}()

	log.Printf("Server started. gRPC on %s, TCP on %s, Admin on %s", listenAddr, tcpAddr, adminAddr)

	// Wait for shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Printf("Shutting down...")
}

var _ = fmt.Sprintf