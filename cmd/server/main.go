package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kaenova/http-tunnels/internal/tunnel"
)

func main() {
	listenAddr := ":8443"
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		listenAddr = v
	}

	tlsCfg := tunnel.DefaultTLSConfig()
	if err := tunnel.EnsureCertificates(tlsCfg); err != nil {
		log.Fatalf("ensuring certificates: %v", err)
	}

	serverTLS, err := tunnel.ServerTLSConfig(tlsCfg)
	if err != nil {
		log.Fatalf("server TLS config: %v", err)
	}

	listener, err := tunnel.ListenTLS(listenAddr, serverTLS)
	if err != nil {
		log.Fatalf("listening: %v", err)
	}
	log.Printf("Server listening on %s", listenAddr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sessions := make(chan *tunnel.Session, 10)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				if isClosedError(err) {
					return
				}
				log.Printf("accept error: %v", err)
				continue
			}
			go handleConn(ctx, conn, sessions)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Printf("Shutting down...")
	cancel()
}

func handleConn(ctx context.Context, conn net.Conn, sessions chan *tunnel.Session) {
	defer conn.Close()

	// Read first byte to detect HTTP/2 vs HTTP/1.1
	// HTTP/2 preface starts with 'P' (0x50 = 'P' from "PRI * HTTP/2.0")
	// HTTP/1.1 starts with method letter (G, P, D, H, C, O, etc)
	oneByte := make([]byte, 1)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, err := io.ReadFull(conn, oneByte)
	if err != nil {
		return
	}
	conn.SetReadDeadline(time.Time{})

	isH2Tunnel := oneByte[0] == 'P'

	if isH2Tunnel {
		// HTTP/2 preface — tunnel client
		// Read remaining 23 bytes of the 24-byte preface
		rest := make([]byte, 23)
		_, err := io.ReadFull(conn, rest)
		if err != nil {
			return
		}
		preface := append(oneByte, rest...)
		if string(preface) != "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n" {
			log.Printf("invalid HTTP/2 preface")
			return
		}

		// Reset deadline before handing off to NewServerSession
		conn.SetReadDeadline(time.Time{})

		session, err := tunnel.NewServerSession(conn)
		if err != nil {
			log.Printf("tunnel session error: %v", err)
			return
		}
		log.Printf("New tunnel session established")
		select {
		case sessions <- session:
		default:
			log.Printf("Session channel full, closing session")
			session.Close()
			return
		}
		<-session.Closed()
	} else {
		// HTTP/1.1 — prepend the first byte and read the request line
		peeked := bufio.NewReader(conn)
		restOfLine, err := peeked.ReadString('\n')
		if err != nil {
			return
		}
		// Reconstruct the request line
		reqLine := string(oneByte) + restOfLine
		reqLine = strings.TrimRight(reqLine, "\r\n")

		// Parse request line: METHOD PATH HTTP/1.1
		parts := strings.SplitN(reqLine, " ", 3)
		if len(parts) < 2 {
			return
		}
		method := parts[0]
		path := parts[1]

		// Read headers
		headers := make(map[string]string)
		for {
			line, err := peeked.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			kv := strings.SplitN(line, ": ", 2)
			if len(kv) == 2 {
				headers[kv[0]] = kv[1]
			}
		}

		// Proxy via tunnel session
		select {
		case session := <-sessions:
			proxySimple(session, conn, method, path, headers)
			select {
			case sessions <- session:
			default:
			}
		default:
			resp := "No tunnel client connected\n"
			fmt.Fprintf(conn, "HTTP/1.1 503 Service Unavailable\r\nContent-Length: %d\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\n%s", len(resp), resp)
		}
	}
}

func proxySimple(session *tunnel.Session, conn net.Conn, method, path string, headers map[string]string) {
	// Build tunnel request header
	tunnelReq := tunnel.RequestHeader{
		Method:  method,
		Path:    path,
		Headers: make(map[string][]string),
	}
	for k, v := range headers {
		tunnelReq.Headers[k] = []string{v}
	}

	// Send through tunnel (no body for GET)
	tunnelResp, err := session.SendRequest(context.Background(), tunnelReq, nil)
	if err != nil {
		resp := fmt.Sprintf("tunnel error: %v\n", err)
		fmt.Fprintf(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: %d\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\n%s", len(resp), resp)
		return
	}

	// Write HTTP/1.1 response
	fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\n", tunnelResp.Status, http.StatusText(tunnelResp.Status))
	for k, vals := range tunnelResp.Headers {
		for _, v := range vals {
			fmt.Fprintf(conn, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprintf(conn, "Content-Length: %d\r\n", len(tunnelResp.Body))
	fmt.Fprintf(conn, "Connection: close\r\n\r\n")
	if len(tunnelResp.Body) > 0 {
		conn.Write(tunnelResp.Body)
	}
}

func isClosedError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "EOF")
}

var (
	_ = tls.Listen
	_ = time.Second
	_ = http.StatusText
)