package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/kaenova/http-tunnels/internal/protocol"
)

var version = "dev"

func main() {
	host := flag.String("host", "https://t.kaenova.my.id", "Tunnel server URL")
	subdomain := flag.String("subdomain", "", "Requested subdomain")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("http-tunnels v5 %s\n", version)
		os.Exit(0)
	}

	if !*verbose {
		log.SetOutput(io.Discard)
	}

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: http-tunnels [flags] <backend_url>")
		fmt.Fprintln(os.Stderr, "  backend_url: http://localhost:3000")
		flag.PrintDefaults()
		os.Exit(1)
	}

	backendURL := flag.Arg(0)
	if !strings.HasPrefix(backendURL, "http://") && !strings.HasPrefix(backendURL, "https://") {
		backendURL = "http://" + backendURL
	}

	// Override host from env
	hostStr := *host
	if envHost := os.Getenv("TUNNEL_HOST"); envHost != "" {
		hostStr = envHost
	}

	// Parse tunnel URL
	tunnelURL, err := url.Parse(hostStr)
	if err != nil {
		log.Fatalf("Invalid tunnel host: %v", err)
	}

	// 1. Create tunnel
	createURL := fmt.Sprintf("%s://%s/new_tunnel", tunnelURL.Scheme, tunnelURL.Host)
	if *subdomain != "" {
		createURL += "?subdomain=" + url.QueryEscape(*subdomain)
	}

	resp, err := http.Post(createURL, "application/json", nil)
	if err != nil {
		log.Fatalf("Failed to create tunnel: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Fatalf("Tunnel creation failed: %s: %s", resp.Status, string(body))
	}

	// Parse response (simple manual parse)
	body, _ := io.ReadAll(resp.Body)
	domain := extractJSON(body, "domain")
	domainKey := extractJSON(body, "domain_key")

	if domain == "" || domainKey == "" {
		log.Fatalf("Invalid tunnel response: %s", string(body))
	}

	log.Printf("Tunnel created: %s", domain)

	// 2. Connect main WebSocket
	wsScheme := "ws"
	if tunnelURL.Scheme == "https" {
		wsScheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/tunnel?domain=%s&domain_key=%s",
		wsScheme, tunnelURL.Host, url.QueryEscape(domain), url.QueryEscape(domainKey))

	dialer := websocket.Dialer{EnableCompression: true}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		log.Fatalf("WebSocket connection failed: %v", err)
	}
	defer conn.Close()

	ws := protocol.NewConnection(conn)

	// Send REGISTER
	if err := ws.Send(&protocol.Frame{
		Type:      protocol.FrameType_REGISTER,
		Domain:    domain,
		DomainKey: domainKey,
	}); err != nil {
		log.Fatalf("Register failed: %v", err)
	}

	// Read REGISTERED
	frame, err := ws.ReadFrame()
	if err != nil || frame.GetType() != protocol.FrameType_REGISTERED {
		log.Fatalf("Registration failed: %v", err)
	}

	log.Printf("Connected! Tunnel: %s", domain)
	log.Printf("Proxying to: %s", backendURL)

	// Handle shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Println("Shutting down...")
		conn.Close()
		os.Exit(0)
	}()

	// 3. Main loop: handle incoming requests
	httpClient := &http.Client{}
	for {
		frame, err := ws.ReadFrame()
		if err != nil {
			log.Printf("Connection closed: %v", err)
			return
		}

		if frame.GetType() == protocol.FrameType_REQUEST {
			go handleRequest(ws, frame, backendURL, domainKey, hostStr, httpClient)
		} else if frame.GetType() == protocol.FrameType_PING {
			ws.Send(&protocol.Frame{Type: protocol.FrameType_PONG})
		}
	}
}

func handleRequest(mainWS *protocol.Connection, reqFrame *protocol.Frame, backendURL, domainKey, hostStr string, httpClient *http.Client) {
	requestID := reqFrame.GetRequestId()
	method := reqFrame.GetMethod()
	path := reqFrame.GetPath()

	if method == "" {
		method = "GET"
	}

	log.Printf("→ %s %s", method, path)

	// Connect dedicated WS
	tunnelURL, _ := url.Parse(hostStr)
	wsScheme := "ws"
	if tunnelURL.Scheme == "https" {
		wsScheme = "wss"
	}
	dedWSURL := fmt.Sprintf("%s://%s/tunnel-response?request_id=%s&domain_key=%s",
		wsScheme, tunnelURL.Host, url.QueryEscape(requestID), url.QueryEscape(domainKey))

	dialer := websocket.Dialer{EnableCompression: true}
	dedConn, _, err := dialer.Dial(dedWSURL, nil)
	if err != nil {
		log.Printf("Dedicated WS failed: %v", err)
		mainWS.Send(&protocol.Frame{
			Type:      protocol.FrameType_REQUEST_ERROR,
			RequestId: requestID,
			Status:    502,
			Error:     err.Error(),
		})
		return
	}
	defer dedConn.Close()

	dedWS := protocol.NewConnection(dedConn)

	// Read request body from dedicated WS
	var reqBody []byte
	for {
		bodyFrame, err := dedWS.ReadFrame()
		if err != nil {
			break
		}
		if bodyFrame.GetType() == protocol.FrameType_BODY_END {
			break
		}
		if bodyFrame.GetType() == protocol.FrameType_BODY {
			reqBody = append(reqBody, bodyFrame.GetChunk()...)
		}
	}

	// Proxy to backend
	var bodyReader io.Reader
	if len(reqBody) > 0 {
		bodyReader = strings.NewReader(string(reqBody))
	}

	req, err := http.NewRequest(method, backendURL+path, bodyReader)
	if err != nil {
		dedWS.Send(&protocol.Frame{
			Type:      protocol.FrameType_RESPONSE_ERROR,
			RequestId: requestID,
			Status:    502,
			Error:     err.Error(),
		})
		return
	}

	// Forward headers
	for k, v := range reqFrame.GetHeaders() {
		for _, val := range v.GetValues() {
			req.Header.Add(k, val)
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		dedWS.Send(&protocol.Frame{
			Type:      protocol.FrameType_RESPONSE_ERROR,
			RequestId: requestID,
			Status:    502,
			Error:     err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	// Send response headers
	respHeaders := make(map[string]*protocol.StringList)
	for k, vals := range resp.Header {
		respHeaders[k] = &protocol.StringList{Values: vals}
	}

	dedWS.Send(&protocol.Frame{
		Type:            protocol.FrameType_RESPONSE_START,
		RequestId:       requestID,
		Status:          int32(resp.StatusCode),
		ResponseHeaders: respHeaders,
	})

	// Stream response body
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			dedWS.Send(&protocol.Frame{
				Type:      protocol.FrameType_RESPONSE_BODY,
				RequestId: requestID,
				Chunk:     chunk,
			})
		}
		if err != nil {
			break
		}
	}

	dedWS.Send(&protocol.Frame{
		Type:      protocol.FrameType_RESPONSE_END,
		RequestId: requestID,
	})

	log.Printf("← %s %s → %d", method, path, resp.StatusCode)
}

// Simple JSON string extractor (avoids importing encoding/json for small payloads)
func extractJSON(data []byte, key string) string {
	search := `"` + key + `":"`
	idx := strings.Index(string(data), search)
	if idx < 0 {
		return ""
	}
	start := idx + len(search)
	end := strings.Index(string(data[start:]), `"`)
	if end < 0 {
		return ""
	}
	return string(data[start : start+end])
}