package tcp

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"sync/atomic"
	"time"

	"github.com/kaenova/http-tunnels/internal/grpc"
)

// Forwarder accepts incoming TCP connections and forwards them through the gRPC tunnel
type Forwarder struct {
	server     *grpc.Server
	connID     atomic.Int64
}

// NewForwarder creates a new TCP forwarder
func NewForwarder(server *grpc.Server) *Forwarder {
	return &Forwarder{
		server: server,
	}
}

// Serve starts accepting TCP connections on the given listener
func (f *Forwarder) Serve(lis net.Listener) error {
	for {
		conn, err := lis.Accept()
		if err != nil {
			if isClosedError(err) {
				return nil
			}
			log.Printf("TCP accept error: %v", err)
			continue
		}
		go f.handleConn(conn)
	}
}

func (f *Forwarder) handleConn(conn net.Conn) {
	defer conn.Close()

	log.Printf("TCP connection accepted from %s", conn.RemoteAddr())

	// Peek at first bytes to detect gRPC vs regular TCP
	peeked := bufio.NewReader(conn)

	isGRPC, err := f.isGRPCConnection(peeked)
	if err != nil {
		log.Printf("peek error: %v", err)
		return
	}

	if isGRPC {
		log.Printf("gRPC connection detected, skipping")
		return
	}

	log.Printf("Regular TCP connection detected, forwarding through tunnel")

	session := f.server.GetDefaultSession()
	if session == nil {
		log.Printf("No tunnel session available")
		response := "HTTP/1.1 503 Service Unavailable\r\nContent-Length: 27\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\nNo tunnel client connected\n"
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		conn.Write([]byte(response))
		return
	}

	log.Printf("Found session: subdomain=%s", session.Subdomain)

	connID := fmt.Sprintf("conn-%d", f.connID.Add(1))
	session.AddBrowserConn(connID, conn)

	if err := session.SendTcpOpen(connID); err != nil {
		log.Printf("SendTcpOpen error: %v", err)
		session.RemoveBrowserConn(connID)
		return
	}

	log.Printf("Sent TcpOpen for conn=%s", connID)

	// Forward data using the buffered reader (has peeked bytes)
	f.forwardData(session, connID, peeked)

	session.RemoveBrowserConn(connID)
}

func (f *Forwarder) forwardData(session *grpc.Session, connID string, peeked *bufio.Reader) {
	buf := make([]byte, 32768)

	for {
		n, err := peeked.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("TCP read error (conn=%s): %v", connID, err)
				session.SendTcpClose(connID, fmt.Sprintf("read error: %v", err))
			} else {
				log.Printf("TCP EOF (conn=%s)", connID)
				session.SendTcpClose(connID, "")
			}
			return
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		log.Printf("Forwarding %d bytes from TCP (conn=%s)", n, connID)
		if err := session.SendTcpData(connID, data); err != nil {
			log.Printf("SendTcpData error: %v", err)
			return
		}
	}
}

func (f *Forwarder) isGRPCConnection(peeked *bufio.Reader) (bool, error) {
	header, err := peeked.Peek(5)
	if err != nil {
		return false, err
	}

	if string(header) == "PRI *" {
		return true, nil
	}

	return false, nil
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