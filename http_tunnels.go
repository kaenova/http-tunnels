package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type AppConfig struct {
	Subdomain         string
	TunnelServer      url.URL
	DestinationServer url.URL
}

type TunnelConfig struct {
	Domain    string
	DomainKey string
}

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

type WebsocketManager struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func NewWebsocketManager(conn *websocket.Conn) *WebsocketManager {
	return &WebsocketManager{
		conn: conn,
	}
}

func (wm *WebsocketManager) ReadMessage() (RequestMessage, error) {
	_, msg, err := wm.conn.ReadMessage()
	if err != nil {
		return RequestMessage{}, err
	}

	var req RequestMessage
	if err := json.Unmarshal(msg, &req); err != nil {
		return RequestMessage{}, err
	}

	return req, nil
}

func (wm *WebsocketManager) SendMessage(msg ResponseMessage) error {
	respBytes, _ := json.Marshal(msg)

	wm.mu.Lock()
	defer wm.mu.Unlock()
	err := wm.conn.WriteMessage(websocket.TextMessage, respBytes)
	if err != nil {
		return err
	}
	return nil
}

func GetAppConfig() *AppConfig {
	// Read environment variable for tunnel host
	tunnelHost := os.Getenv("TUNNEL_HOST")

	// Flags for subdomain
	subdomainFlag := flag.String("subdomain", "", "Subdomain to use for the tunnel")

	// Flags for tunnel host and scheme
	hostFlag := flag.String("host", tunnelHost, "Public Tunnel host address (ex. http://tunnel.example.com) or use TUNNEL_HOST env var")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Tunnel Client - A simple HTTP tunnel client")
		fmt.Fprintln(os.Stderr, "Github : https://github.com/kaenova/http-tunnels")
		fmt.Fprintln(os.Stderr, "Usage: tunnel-client [options] <destination_server>")
		fmt.Fprintln(os.Stderr, "Options:")
		flag.PrintDefaults()
	}
	flag.Parse()

	destinationServer := flag.Arg(0)
	if destinationServer == "" {
		log.Fatal("Destination server is required")
	}

	destinationUrl, err := url.Parse(destinationServer)
	if err != nil {
		log.Fatal("Invalid tunnel host URL:", err)
	}

	if tunnelHost == "" {
		if hostFlag == nil || *hostFlag == "" {
			log.Fatal("Tunnel host is required")
		}
	}

	// Parse tunnel host to tunnelHost and tunnelScheme
	tunnelURL, err := url.Parse(*hostFlag)
	if err != nil {
		log.Fatal("Invalid tunnel host URL:", err)
	}
	if (tunnelURL.Scheme != "http" && tunnelURL.Scheme != "https") || tunnelURL.Host == "" {
		log.Fatal("Invalid tunnel host URL (ex. http(s)://tunner.domain.com) :", *hostFlag)
	}

	if destinationUrl.Scheme != "http" && destinationUrl.Scheme != "https" {
		log.Fatal("Invalid destination server URL (ex. http(s)://destination.domain.com or http(s)://localhost) :", destinationServer)
	}

	subdomain := ""
	if subdomainFlag != nil && *subdomainFlag != "" {
		subdomain = *subdomainFlag
	}

	return &AppConfig{
		Subdomain:         subdomain,
		TunnelServer:      *tunnelURL,
		DestinationServer: *destinationUrl,
	}

}

func RequestNewTunnel(config *AppConfig) TunnelConfig {
	// Request for new tunnel
	rawQuery := ""
	if config.Subdomain != "" {
		rawQuery = "subdomain=" + config.Subdomain
	}
	uNewTunnel := url.URL{Scheme: config.TunnelServer.Scheme, Host: config.TunnelServer.Host, Path: "/new_tunnel", RawQuery: rawQuery}
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

	return TunnelConfig{
		Domain:    tunnelResp.Domain,
		DomainKey: tunnelResp.DomainKey,
	}
}

func StartTunnelServer(config *AppConfig, tunConfig TunnelConfig) {

	uTunnel := url.URL{Scheme: "ws", Host: config.TunnelServer.Host, Path: "/tunnel", RawQuery: "domain=" + tunConfig.Domain + "&domain_key=" + tunConfig.DomainKey}
	conn, _, err := websocket.DefaultDialer.Dial(uTunnel.String(), nil)
	if err != nil {
		log.Fatal("Dial error:", err)
	}

	defer conn.Close()

	log.Println("Connected to tunnel server")

	wsM := NewWebsocketManager(conn)

	for {
		requestMsg, err := wsM.ReadMessage()
		if err != nil {
			log.Println("Read error:", err)
			wsM.SendMessage(ResponseMessage{
				ID:     requestMsg.ID,
				Status: http.StatusInternalServerError,
				Body:   []byte("Internal server error"),
			})
			break
		}

		go handleRequest(config, wsM, requestMsg)
	}

}

func handleRequest(config *AppConfig, wsM *WebsocketManager, req RequestMessage) {
	log.Printf("Handling request: %s %s", req.Method, req.Path)

	localURL := config.DestinationServer.String() + req.Path

	httpReq, _ := http.NewRequest(req.Method, localURL, bytes.NewReader(req.Body))
	for k, vv := range req.Headers {
		for _, v := range vv {
			httpReq.Header.Add(k, v)
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)

	if err != nil {
		wsM.SendMessage(ResponseMessage{
			ID:     req.ID,
			Body:   []byte(err.Error()),
			Status: http.StatusBadGateway,
		})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	// Send the response back to the client
	wsM.SendMessage(ResponseMessage{
		ID:      req.ID,
		Status:  resp.StatusCode,
		Headers: resp.Header,
		Body:    body,
	})
}

func main() {

	config := GetAppConfig()

	// Request a new tunnel
	tunnelResp := RequestNewTunnel(config)

	// Start the tunnel server
	StartTunnelServer(config, tunnelResp)

}
