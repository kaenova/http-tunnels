package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kaenova/http-tunnels/internal/client"
	"github.com/kaenova/http-tunnels/internal/server"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func startBackend(port string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[BACKEND] WS upgrade from %s", r.RemoteAddr)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[BACKEND] upgrade failed: %v", err)
			return
		}
		defer conn.Close()
		log.Printf("[BACKEND] WS connected")
		for {
			msgType, msg, err := conn.ReadMessage()
			if err != nil {
				log.Printf("[BACKEND] read error: %v", err)
				return
			}
			log.Printf("[BACKEND] received: %s", string(msg))
			if err := conn.WriteMessage(msgType, msg); err != nil {
				log.Printf("[BACKEND] write error: %v", err)
				return
			}
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello from backend"))
	})
	srv := &http.Server{Addr: "127.0.0.1:" + port, Handler: mux}
	go srv.ListenAndServe()
	return srv
}

func main() {
	// Cleanup old DB
	os.Remove("/tmp/http-tunnels-test/e2e.db")

	// Start tunnel server
	app, err := server.NewApp(server.Config{
		ListenAddr:            "127.0.0.1:18080",
		TunnelDomain:          "t.localhost",
		DBPath:                "/tmp/http-tunnels-test/e2e.db",
		MaxConcurrentRequests: 100,
		DefaultRequestTimeout: 30000,
		DefaultBackendTimeout: 30000,
	}, nil)
	if err != nil {
		log.Fatalf("Server create failed: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:18080")
	if err != nil {
		log.Fatalf("Server listen failed: %v", err)
	}
	go app.Serve(listener)
	log.Printf("[SERVER] Started on 127.0.0.1:18080")

	// Start backend
	backend := startBackend("18082")
	log.Printf("[BACKEND] Started on 127.0.0.1:18082")
	defer backend.Close()

	// Wait for services
	time.Sleep(1 * time.Second)

	// Start tunnel client in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := client.Run(ctx, client.Options{
			Host:       "http://127.0.0.1:18080",
			BackendURL: "http://127.0.0.1:18082",
			Subdomain:  "ws-e2e",
		}); err != nil {
			log.Printf("[CLIENT] exited: %v", err)
		}
	}()

	// Wait for client registration
	time.Sleep(2 * time.Second)

	// Test 1: HTTP through tunnel
	log.Printf("[TEST] Testing HTTP...")
	req, _ := http.NewRequest("GET", "http://127.0.0.1:18080/", nil)
	req.Host = "ws-e2e.t.localhost"
	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Fatalf("[TEST] HTTP request failed: %v", err)
	}
	body := make([]byte, 256)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	log.Printf("[TEST] HTTP status=%d body=%s", resp.StatusCode, string(body[:n]))
	if resp.StatusCode != 200 || string(body[:n]) != "hello from backend" {
		log.Fatalf("[TEST] HTTP FAILED")
	}
	log.Printf("[TEST] HTTP PASSED")

	// Test 2: WebSocket through tunnel
	log.Printf("[TEST] Testing WebSocket...")
	dialer := websocket.Dialer{
		NetDial: func(network, addr string) (net.Conn, error) {
			return net.Dial(network, "127.0.0.1:18080")
		},
		HandshakeTimeout: 10 * time.Second,
	}
	headers := http.Header{}
	headers.Set("Host", "ws-e2e.t.localhost")
	wsConn, resp, err := dialer.Dial("ws://ws-e2e.t.localhost:18080/ws", headers)
	if err != nil {
		if resp != nil {
			body := make([]byte, 1024)
			n, _ := resp.Body.Read(body)
			log.Printf("[TEST] WS dial failed status=%d body=%s", resp.StatusCode, string(body[:n]))
		}
		log.Fatalf("[TEST] WS dial failed: %v", err)
	}
	defer wsConn.Close()
	log.Printf("[TEST] WS connected!")

	msg := "hello from test"
	if err := wsConn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		log.Fatalf("[TEST] WS write failed: %v", err)
	}
	log.Printf("[TEST] WS sent: %s", msg)

	wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, reply, err := wsConn.ReadMessage()
	if err != nil {
		log.Fatalf("[TEST] WS read failed: %v", err)
	}
	log.Printf("[TEST] WS received: %s", string(reply))

	if string(reply) == msg {
		fmt.Println("SUCCESS: WebSocket echo passed")
	} else {
		fmt.Println("FAILURE: unexpected response")
	}

	cancel()
	app.Shutdown()
}
