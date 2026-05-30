package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kaenova/http-tunnels/internal/grpc"
)

func main() {
	host := flag.String("host", "t.kaenova.my.id:443", "Tunnel server host:port")
	subdomain := flag.String("subdomain", "", "Requested subdomain (optional)")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		log.Fatal("Usage: http-tunnels [flags] <backend_addr>\n  backend_addr: host:port (e.g. 127.0.0.1:5000)")
	}

	backendAddr := args[0]
	parts := strings.Split(backendAddr, ":")
	if len(parts) != 2 {
		log.Fatalf("Invalid backend address: %s (expected host:port)", backendAddr)
	}

	backendHost := parts[0]
	backendPort := int32(0)
	fmt.Sscanf(parts[1], "%d", &backendPort)

	if backendPort == 0 {
		log.Fatalf("Invalid backend port: %s", parts[1])
	}

	if !*verbose {
		log.SetOutput(io.Discard)
	}

	client := grpc.NewClient()
	defer client.Close()

	// Determine if TLS is needed
	useTLS := true
	addr := *host
	if strings.HasPrefix(addr, "http://") {
		useTLS = false
		addr = strings.TrimPrefix(addr, "http://")
	} else if strings.HasPrefix(addr, "https://") {
		useTLS = true
		addr = strings.TrimPrefix(addr, "https://")
	}

	if err := client.Connect(addr, useTLS, *subdomain, backendHost, backendPort); err != nil {
		log.Fatalf("Connection failed: %v", err)
	}

	fmt.Printf("Connected! Subdomain: %s\n", client.Subdomain())
	fmt.Println("Waiting for requests...")

	// Handle shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sig
		client.Close()
		os.Exit(0)
	}()

	// Run the client stream loop
	if err := client.Run(); err != nil {
		log.Printf("Client error: %v", err)
	}
}