package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/kaenova/http-tunnels/internal/tunnel"
)

func main() {
	tunnelAddr := getEnv("TUNNEL_ADDR", "127.0.0.1:50051")
	destAddr := getEnv("DEST_ADDR", "http://127.0.0.1:5000")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	clientTLS := tunnel.ClientTLSConfig()

	fmt.Printf("Connecting to %s...\n", tunnelAddr)
	session, err := tunnel.DialTunnel(ctx, tunnelAddr, clientTLS)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	fmt.Printf("Connected!\n")

	// Read incoming requests
	go func() {
		fmt.Printf("Waiting for requests...\n")
		for req := range session.IncomingRequests() {
			fmt.Printf("GOT REQUEST: %s %s\n", req.Header.Method, req.Header.Path)

			url := destAddr + req.Header.Path
			httpReq, _ := http.NewRequest(req.Header.Method, url, nil)
			resp, err := http.DefaultClient.Do(httpReq)
			if err != nil {
				fmt.Printf("proxy error: %v\n", err)
				session.SendResponse(req.StreamID, tunnel.ResponseHeader{
					Status: 502,
				}, []byte(err.Error()))
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			respHeaders := make(map[string][]string)
			for k, vals := range resp.Header {
				respHeaders[k] = vals
			}

			session.SendResponse(req.StreamID, tunnel.ResponseHeader{
				Status:  resp.StatusCode,
				Headers: respHeaders,
			}, body)

			fmt.Printf("Sent response: %d (%d bytes)\n", resp.StatusCode, len(body))
		}
	}()

	time.Sleep(10 * time.Second)
	session.Close()
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}