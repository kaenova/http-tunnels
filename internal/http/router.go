package http

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kaenova/http-tunnels/internal/grpc"
)

// Router handles HTTP connections and routes to admin or tunnel proxy
type Router struct {
	adminHandler http.Handler
	tunnelServer *grpc.Server
	connID       atomic.Int64
}

// NewRouter creates a new HTTP router
func NewRouter(tunnelServer *grpc.Server, adminHandler http.Handler) *Router {
	return &Router{
		adminHandler: adminHandler,
		tunnelServer: tunnelServer,
	}
}

// Serve starts accepting HTTP connections on the given listener
func (r *Router) Serve(lis net.Listener) error {
	for {
		conn, err := lis.Accept()
		if err != nil {
			if isClosedError(err) {
				return nil
			}
			log.Printf("HTTP accept error: %v", err)
			continue
		}
		go r.handleConn(conn)
	}
}

// ServeChan accepts HTTP connections from a channel
func (r *Router) ServeChan(connChan chan net.Conn) {
	for conn := range connChan {
		go r.handleConn(conn)
	}
}

func (r *Router) handleConn(conn net.Conn) {
	defer conn.Close()

	peeked := bufio.NewReader(conn)
	firstLine, err := peeked.ReadString('\n')
	if err != nil {
		return
	}
	firstLine = strings.TrimSpace(firstLine)

	parts := strings.SplitN(firstLine, " ", 3)
	if len(parts) < 2 {
		return
	}

	method := parts[0]
	path := parts[1]

	if strings.HasPrefix(path, "/admin/") || strings.HasPrefix(path, "/api/admin/") {
		// Admin request — serve via HTTP
		r.serveAdmin(conn, method, path, peeked)
	} else {
		// Tunnel proxy — prepend the first line back to peeked
		r.forwardToTunnel(conn, firstLine+"\r\n", peeked)
	}
}

func (r *Router) serveAdmin(conn net.Conn, method, path string, peeked *bufio.Reader) {
	// Read remaining headers from the buffered reader
	headers := make(map[string]string)
	contentLength := 0
	for {
		line, err := peeked.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) == 2 {
			headers[parts[0]] = parts[1]
			if strings.ToLower(parts[0]) == "content-length" {
				fmt.Sscanf(parts[1], "%d", &contentLength)
			}
		}
	}

	// Read body if present
	var body io.ReadCloser
	if contentLength > 0 {
		bodyBytes := make([]byte, contentLength)
		_, err := io.ReadFull(peeked, bodyBytes)
		if err != nil {
			return
		}
		body = io.NopCloser(bytes.NewReader(bodyBytes))
	} else {
		body = http.NoBody
	}

	// Build a minimal HTTP request for the admin handler
	req := &http.Request{
		Method: method,
		URL:    &url.URL{Path: path},
		Proto:  "HTTP/1.1",
		Header: make(http.Header),
		Host:   headers["Host"],
		Body:   body,
		ContentLength: int64(contentLength),
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rw := &connResponseWriter{conn: conn, header: make(http.Header)}
	r.adminHandler.ServeHTTP(rw, req)
	rw.finalize()
}

func (r *Router) forwardToTunnel(conn net.Conn, firstLine string, peeked *bufio.Reader) {
	session := r.tunnelServer.GetDefaultSession()
	if session == nil {
		log.Printf("No tunnel session available for proxy request")
		response := "HTTP/1.1 503 Service Unavailable\r\nContent-Length: 27\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\nNo tunnel client connected\n"
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		conn.Write([]byte(response))
		return
	}

	log.Printf("Forwarding to tunnel session: subdomain=%s", session.Subdomain)

	connID := fmt.Sprintf("conn-%d", r.connID.Add(1))
	session.AddBrowserConn(connID, conn)

	if err := session.SendTcpOpen(connID); err != nil {
		log.Printf("SendTcpOpen error: %v", err)
		session.RemoveBrowserConn(connID)
		return
	}

	log.Printf("Sent TcpOpen for conn=%s", connID)

	// Send the first line first
	if err := session.SendTcpData(connID, []byte(firstLine)); err != nil {
		log.Printf("SendTcpData error: %v", err)
		session.RemoveBrowserConn(connID)
		return
	}

	// Forward remaining data from the buffered reader
	r.forwardData(session, connID, peeked)

	session.RemoveBrowserConn(connID)
}

func (r *Router) forwardData(session *grpc.Session, connID string, peeked *bufio.Reader) {
	buf := make([]byte, 32768)

	for {
		n, err := peeked.Read(buf)
		if err != nil {
			if err != io.EOF {
				session.SendTcpClose(connID, fmt.Sprintf("read error: %v", err))
			} else {
				session.SendTcpClose(connID, "")
			}
			return
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		if err := session.SendTcpData(connID, data); err != nil {
			return
		}
	}
}

// connResponseWriter implements http.ResponseWriter over a net.Conn
type connResponseWriter struct {
	conn   net.Conn
	header http.Header
	status int
	wrote  bool
}

func (w *connResponseWriter) Header() http.Header {
	return w.header
}

func (w *connResponseWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.conn.Write(data)
}

func (w *connResponseWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.status = status

	statusText := http.StatusText(status)
	fmt.Fprintf(w.conn, "HTTP/1.1 %d %s\r\n", status, statusText)

	for k, vals := range w.header {
		for _, v := range vals {
			fmt.Fprintf(w.conn, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprintf(w.conn, "Connection: close\r\n\r\n")
}

func (w *connResponseWriter) finalize() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
}

func isClosedError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return s == "use of closed network connection" ||
		s == "connection reset" ||
		s == "broken pipe"
}

var _ = time.Second