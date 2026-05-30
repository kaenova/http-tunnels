package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/kaenova/http-tunnels/internal/tunnel"
)

func main() {
	tunnelAddr := getEnv("TUNNEL_ADDR", "127.0.0.1:50051")
	destAddr := getEnv("DEST_ADDR", "http://127.0.0.1:5000")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientTLS := tunnel.ClientTLSConfig()

	session, err := tunnel.DialTunnel(ctx, tunnelAddr, clientTLS)
	if err != nil {
		log.Fatalf("dialing tunnel: %v", err)
	}
	log.Printf("Connected to tunnel server at %s", tunnelAddr)

	// Handle incoming requests from the tunnel
	go func() {
		for req := range session.IncomingRequests() {
			handleRequest(ctx, req, destAddr, session)
		}
	}()

	log.Printf("Tunnel client ready, proxying to %s", destAddr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Printf("Shutting down...")
	session.Close()
}

func handleRequest(ctx context.Context, req *tunnel.IncomingRequest, destAddr string, sess *tunnel.Session) {
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
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}