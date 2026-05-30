package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kaenova/http-tunnels/internal/tunnel"
)

func main() {
	host := flag.String("host", "https://t.kaenova.my.id", "Public tunnel host address")
	subdomain := flag.String("subdomain", "", "Subdomain to use for the tunnel (unused in H2 mode)")
	verbose := flag.Bool("verbose", false, "Enable verbose request/response logging")
	_ = subdomain
	flag.Parse()

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

	// Parse host to extract tunnel server address
	hostURL, err := url.Parse(*host)
	if err != nil {
		log.Fatalf("invalid host: %v", err)
	}

	tunnelHost := hostURL.Host
	if !strings.Contains(tunnelHost, ":") {
		if hostURL.Scheme == "https" {
			tunnelHost = tunnelHost + ":443"
		} else {
			tunnelHost = tunnelHost + ":80"
		}
	}

	// For HTTP/2 tunnel, we connect to port 50051 by default
	// (tunnel server listens on 50051 for H2 connections)
	tunnelAddr := tunnelHost
	if strings.HasSuffix(tunnelAddr, ":443") || strings.HasSuffix(tunnelAddr, ":80") {
		// Replace port with tunnel port
		parts := strings.Split(tunnelAddr, ":")
		tunnelAddr = parts[0] + ":50051"
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

// Ensure net is used
var _ = net.Listen