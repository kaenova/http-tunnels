package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

type RequestMessage struct {
	ID      string              `json:"id"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

type ResponseMessage struct {
	ID      string              `json:"id"`
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

func main() {
	// Flags for subdomain
	subdomainFlag := flag.String("subdomain", "", "Subdomain to use for the tunnel")

	// Flags for tunnel host and scheme
	hostFlag := flag.String("host", "", "Public Tunnel host address (ex. http://tunnel.example.com)")

	flag.Parse()

	if hostFlag == nil || *hostFlag == "" {
		log.Fatal("Tunnel host is required")
	}

	// Parse tunnel host to tunnelHost and tunnelScheme
	tunnelURL, err := url.Parse(*hostFlag)
	if err != nil {
		log.Fatal("Invalid tunnel host URL:", err)
	}
	host := tunnelURL.Host
	scheme := tunnelURL.Scheme

	// Request for new tunnel
	rawQuery := ""
	if subdomainFlag != nil && *subdomainFlag != "" {
		rawQuery = "subdomain=" + *subdomainFlag
	}
	uNewTunnel := url.URL{Scheme: scheme, Host: host, Path: "/new_tunnel", RawQuery: rawQuery}
	resp, err := http.Post(uNewTunnel.String(), "application/json", nil)
	if err != nil {
		log.Fatal("Submitting new domain error:", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Failed to create tunnel: %s", resp.Status)
	}

	// Read the response
	var tunnelResp struct {
		Domain    string `json:"domain"`
		DomainKey string `json:"domain_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tunnelResp); err != nil {
		log.Fatal("Decoding tunnel response error:", err)
	}

	log.Printf("Tunnel created with domain: %s", tunnelResp.Domain)
	log.Printf("Domain key: %s", tunnelResp.DomainKey)

	uTunnel := url.URL{Scheme: "ws", Host: host, Path: "/tunnel", RawQuery: "domain=" + tunnelResp.Domain + "&domain_key=" + tunnelResp.DomainKey}
	conn, _, err := websocket.DefaultDialer.Dial(uTunnel.String(), nil)
	if err != nil {
		log.Fatal("Dial error:", err)
	}

	defer conn.Close()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("Read error:", err)
			return
		}

		var req RequestMessage
		if err := json.Unmarshal(msg, &req); err != nil {
			continue
		}

		go handleRequest(conn, req)
	}
}

func handleRequest(conn *websocket.Conn, req RequestMessage) {
	localURL := "http://localhost:5500" + req.Path
	httpReq, _ := http.NewRequest(req.Method, localURL, bytes.NewReader(req.Body))

	for k, vv := range req.Headers {
		for _, v := range vv {
			httpReq.Header.Add(k, v)
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		sendError(conn, req.ID, err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	respMsg := ResponseMessage{
		ID:      req.ID,
		Status:  resp.StatusCode,
		Headers: resp.Header,
		Body:    body,
	}
	sendResponse(conn, respMsg)
}

func sendResponse(conn *websocket.Conn, resp ResponseMessage) {
	respBytes, _ := json.Marshal(resp)
	conn.WriteMessage(websocket.TextMessage, respBytes)
}

func sendError(conn *websocket.Conn, reqID string, err error) {
	respMsg := ResponseMessage{
		ID:     reqID,
		Status: http.StatusBadGateway,
		Body:   []byte(err.Error()),
	}
	sendResponse(conn, respMsg)
}
