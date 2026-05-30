package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kaenova/http-tunnels/internal/tunnel"
)

var version = "dev"

func main() {
	host := flag.String("host", "https://t.kaenova.my.id", "Public tunnel host address")
	subdomain := flag.String("subdomain", "", "Subdomain to use for the tunnel (unused in H2 mode)")
	verbose := flag.Bool("verbose", false, "Enable verbose request/response logging")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	_ = subdomain

	if *showVersion {
		fmt.Printf("http-tunnels %s\n", version)
		os.Exit(0)
	}

	// Handle update command
	if flag.NArg() >= 1 && flag.Arg(0) == "update" {
		doUpdate()
		return
	}

	if flag.NArg() < 1 {
		fmt.Println(`http-tunnels - a simple HTTP tunnel client
Github : https://github.com/kaenova/http-tunnels

Usage:
  http-tunnels [options] <destination_server>
  http-tunnels update

Options:
  -host string
        Public tunnel host address (default "https://t.kaenova.my.id" or TUNNEL_HOST env var)
  -subdomain string
        Subdomain to use for the tunnel
  -verbose
        Enable verbose request/response logging`)
		os.Exit(1)
	}

	destAddr := flag.Arg(0)

	// Override host from env
	hostStr := *host
	if envHost := os.Getenv("TUNNEL_HOST"); envHost != "" {
		hostStr = envHost
	}

	// Parse tunnel address
	tunnelAddr := hostStr
	if strings.HasPrefix(hostStr, "https://") || strings.HasPrefix(hostStr, "http://") {
		// Extract host:port
		parts := strings.Split(hostStr, "://")
		hostPart := parts[1]
		if idx := strings.Index(hostPart, "/"); idx > 0 {
			hostPart = hostPart[:idx]
		}
		if !strings.Contains(hostPart, ":") {
			hostPart = hostPart + ":443"
		}
		tunnelAddr = hostPart
	} else if !strings.Contains(tunnelAddr, ":") {
		tunnelAddr = tunnelAddr + ":443"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientTLS := tunnel.ClientTLSConfig()

	session, err := tunnel.DialTunnel(ctx, tunnelAddr, clientTLS)
	if err != nil {
		log.Fatalf("dialing tunnel: %v", err)
	}
	log.Printf("Connected to tunnel server at %s", tunnelAddr)

	// Handle incoming requests
	go func() {
		for req := range session.IncomingRequests() {
			if *verbose {
				log.Printf("[verbose] REQUEST %s %s", req.Header.Method, req.Header.Path)
			}
			handleRequest(ctx, req, destAddr, session, *verbose)
		}
	}()

	log.Printf("Tunnel client ready, proxying to %s", destAddr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Printf("Shutting down...")
	session.Close()
}

func handleRequest(ctx context.Context, req *tunnel.IncomingRequest, destAddr string, sess *tunnel.Session, verbose bool) {
	url := destAddr + req.Header.Path

	httpReq, err := http.NewRequest(req.Header.Method, url, bytes.NewReader(req.Body))
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return
	}

	for k, vals := range req.Header.Headers {
		for _, v := range vals {
			httpReq.Header.Add(k, v)
		}
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		log.Printf("Error proxying to %s: %v", url, err)
		sess.SendResponse(req.StreamID, tunnel.ResponseHeader{
			Status:  502,
			Headers: map[string][]string{"Content-Type": {"text/plain"}},
		}, []byte(fmt.Sprintf("proxy error: %v", err)))
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		return
	}

	respHeaders := make(map[string][]string)
	for k, vals := range resp.Header {
		respHeaders[k] = vals
	}

	sess.SendResponse(req.StreamID, tunnel.ResponseHeader{
		Status:  resp.StatusCode,
		Headers: respHeaders,
	}, body)

	if verbose {
		log.Printf("[verbose] RESPONSE %s %s → %d (%d bytes)", req.Header.Method, req.Header.Path, resp.StatusCode, len(body))
	}
}

func doUpdate() {
	fmt.Println("Update not supported in H2 mode. Use your package manager.")
	os.Exit(1)
}