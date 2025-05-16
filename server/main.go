package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type TunnelManager struct {
	mu      sync.RWMutex
	tunnels map[string]*Tunnel
}

type DomainKeyManager struct {
	mu     sync.RWMutex
	domain map[string]string
}

type Tunnel struct {
	Domain          string
	Conn            *websocket.Conn
	Outgoing        chan []byte
	PendingRequests sync.Map
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

func main() {
	tm := &TunnelManager{
		tunnels: make(map[string]*Tunnel),
	}

	dkm := &DomainKeyManager{
		domain: make(map[string]string),
	}

	http.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	})

	// Handle new tunnel requests
	http.HandleFunc("/new_tunnel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Get query parameters
		queryParams := r.URL.Query()
		reqSubdomain := queryParams.Get("subdomain")

		tm.mu.Lock()

		domain := r.Host

		// if subdomain not provided, generate a random one
		if reqSubdomain == "" {
			for {
				reqSubdomain = generateRandomSubdomain()
				domain = reqSubdomain + "." + r.Host
				if _, exists := tm.tunnels[domain]; !exists {
					break
				}
			}
		}

		if reqSubdomain != "" {
			if _, exists := tm.tunnels[reqSubdomain]; exists {
				http.Error(w, "subdomain already in use", http.StatusBadRequest)
				tm.mu.Unlock()
				return
			}
			domain = reqSubdomain + "." + r.Host
		}

		// Create a new tunnel
		tunnel := &Tunnel{
			Domain:          domain,
			Conn:            nil,
			Outgoing:        make(chan []byte, 10000),
			PendingRequests: sync.Map{},
		}

		tm.tunnels[domain] = tunnel

		dkm.mu.Lock()
		domainKey := generateRequestID()
		dkm.domain[domain] = domainKey
		dkm.mu.Unlock()

		tm.mu.Unlock()

		// Send the domain back to the client
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		response := map[string]string{"domain": domain, "domain_key": domainKey}
		responseBytes, _ := json.Marshal(response)
		w.Write(responseBytes)
		log.Printf("New domain registered: %s", domain)
	})

	// Handle WebSocket connections
	http.HandleFunc("/tunnel", func(w http.ResponseWriter, r *http.Request) {
		// Get request domain from query parameters
		queryParams := r.URL.Query()
		reqDomain := queryParams.Get("domain")
		reqDomainKey := queryParams.Get("domain_key")
		if reqDomain == "" || reqDomainKey == "" {
			http.Error(w, "bot domain and domain_key need provided", http.StatusBadRequest)
			return
		}

		// Check if the domain key is valid
		dkm.mu.RLock()
		expectedKey, exists := dkm.domain[reqDomain]
		dkm.mu.RUnlock()
		if !exists || expectedKey != reqDomainKey {
			http.Error(w, "invalid domain key", http.StatusForbidden)
			return
		}

		// Upgrade the connection to WebSocket
		tm.mu.Lock()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("Upgrade error:", err)
			return
		}
		conn.SetCloseHandler(func(code int, text string) error {
			tm.mu.Lock()
			delete(tm.tunnels, reqDomain)
			tm.mu.Unlock()
			dkm.mu.Lock()
			delete(dkm.domain, reqDomain)
			dkm.mu.Unlock()
			return nil
		})
		defer conn.Close()

		log.Printf("New tunnel created: %s", reqDomain)
		tunnel := &Tunnel{
			Domain:   reqDomain,
			Conn:     conn,
			Outgoing: make(chan []byte, 100),
		}
		tm.tunnels[reqDomain] = tunnel
		tm.mu.Unlock()

		go tunnel.writer()
		tunnel.reader(tm)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		domains := r.Host

		tm.mu.RLock()
		tunnel, exists := tm.tunnels[domains]
		tm.mu.RUnlock()

		if !exists {
			http.Error(w, "Tunnel not found", http.StatusNotFound)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Error reading body", http.StatusInternalServerError)
			return
		}

		reqID := generateRequestID()
		reqMsg := RequestMessage{
			ID:      reqID,
			Method:  r.Method,
			Path:    r.URL.Path,
			Headers: r.Header,
			Body:    body,
		}
		reqBytes, _ := json.Marshal(reqMsg)

		respCh := make(chan ResponseMessage, 1)
		tunnel.PendingRequests.Store(reqID, respCh)
		defer tunnel.PendingRequests.Delete(reqID)

		tunnel.Outgoing <- reqBytes

		select {
		case resp := <-respCh:
			for k, vv := range resp.Headers {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.Status)
			w.Write(resp.Body)
		case <-time.After(30 * time.Second):
			http.Error(w, "Gateway timeout", http.StatusGatewayTimeout)
		}
	})

	log.Println("Listening on :80")
	log.Fatal(http.ListenAndServe(":80", nil))
}

func (t *Tunnel) writer() {
	for msg := range t.Outgoing {
		if err := t.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			break
		}
	}
}

func (t *Tunnel) reader(tm *TunnelManager) {
	defer func() {
		tm.mu.Lock()
		delete(tm.tunnels, t.Domain)
		tm.mu.Unlock()
		close(t.Outgoing)
	}()

	for {
		_, msg, err := t.Conn.ReadMessage()
		if err != nil {
			break
		}

		var resp ResponseMessage
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue
		}

		if ch, ok := t.PendingRequests.LoadAndDelete(resp.ID); ok {
			ch.(chan ResponseMessage) <- resp
		}
	}
}

func generateRandomSubdomain() string {
	b := make([]byte, 8)
	rand.Read(b)
	return strings.ToLower(base64.RawURLEncoding.EncodeToString(b))
}

func generateRequestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
