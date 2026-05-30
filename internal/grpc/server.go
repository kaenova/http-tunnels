package grpc

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	pb "github.com/kaenova/http-tunnels/internal/grpc/pb"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// Session represents a connected tunnel client
type Session struct {
	Subdomain   string
	BackendHost string
	BackendPort int32
	Stream      pb.TunnelService_TunnelServer
	ConnectedAt time.Time
	mu          sync.Mutex

	// Backend TCP connections (client side)
	backendConns map[string]net.Conn
	backendMu    sync.Mutex

	// Browser TCP connections (server side)
	browserConns map[string]net.Conn
	browserMu    sync.Mutex
}

// Server manages all tunnel sessions
type Server struct {
	pb.UnimplementedTunnelServiceServer
	sessions map[string]*Session // subdomain -> session
	mu       sync.RWMutex
}

func NewServer() *Server {
	return &Server{
		sessions: make(map[string]*Session),
	}
}

// Tunnel implements the bidirectional tunnel stream
func (s *Server) Tunnel(stream pb.TunnelService_TunnelServer) error {
	// Wait for RegisterRequest
	msg, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receiving register: %w", err)
	}

	reg := msg.GetRegister()
	if reg == nil {
		return fmt.Errorf("first message must be RegisterRequest")
	}

	// Generate or use requested subdomain
	subdomain := reg.Subdomain
	if subdomain == "" {
		// TODO: use name generator
		subdomain = fmt.Sprintf("tunnel-%d", time.Now().UnixNano()%10000)
	}

	session := &Session{
		Subdomain:   subdomain,
		BackendHost: reg.BackendHost,
		BackendPort: reg.BackendPort,
		Stream:      stream,
		ConnectedAt: time.Now(),
		backendConns: make(map[string]net.Conn),
		browserConns: make(map[string]net.Conn),
	}

	// Register session
	s.mu.Lock()
	s.sessions[subdomain] = session
	s.mu.Unlock()

	// Send ack
	err = stream.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_RegisterAck{
			RegisterAck: &pb.RegisterResponse{
				AssignedSubdomain: subdomain,
				Success:           true,
			},
		},
	})
	if err != nil {
		s.removeSession(subdomain)
		return fmt.Errorf("sending register ack: %w", err)
	}

	if p, ok := peer.FromContext(stream.Context()); ok {
		log.Printf("Client registered: subdomain=%s backend=%s:%d from=%s",
			subdomain, reg.BackendHost, reg.BackendPort, p.Addr)
	} else {
		log.Printf("Client registered: subdomain=%s backend=%s:%d",
			subdomain, reg.BackendHost, reg.BackendPort)
	}

	// Stream loop: receive messages from client
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Client %s stream error: %v", subdomain, err)
			break
		}

		switch payload := msg.Payload.(type) {
		case *pb.TunnelMessage_Data:
			session.handleTcpData(payload.Data)
		case *pb.TunnelMessage_Close:
			session.handleTcpClose(payload.Close)
		case *pb.TunnelMessage_Pong:
			// Health check response, ignore
		}
	}

	s.removeSession(subdomain)
	log.Printf("Client disconnected: subdomain=%s", subdomain)
	return nil
}

func (s *Server) removeSession(subdomain string) {
	s.mu.Lock()
	delete(s.sessions, subdomain)
	s.mu.Unlock()
}

// GetSession returns a session by subdomain
func (s *Server) GetSession(subdomain string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[subdomain]
}

// GetDefaultSession returns the first available session (for single-client setups)
func (s *Server) GetDefaultSession() *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, session := range s.sessions {
		return session
	}
	return nil
}

// SessionCount returns number of active sessions
func (s *Server) SessionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// ListSessions returns all sessions
func (s *Server) ListSessions() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		result = append(result, session)
	}
	return result
}

// SendTcpOpen sends a TcpOpen message to the client
func (sess *Session) SendTcpOpen(connID string) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.Stream.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_Open{
			Open: &pb.TcpOpen{
				ConnectionId: connID,
			},
		},
	})
}

// SendTcpData sends a TcpData message to the client
func (sess *Session) SendTcpData(connID string, data []byte) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.Stream.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_Data{
			Data: &pb.TcpData{
				ConnectionId: connID,
				Data:         data,
			},
		},
	})
}

// SendTcpClose sends a TcpClose message to the client
func (sess *Session) SendTcpClose(connID string, errMsg string) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	closeMsg := &pb.TcpClose{
		ConnectionId: connID,
	}
	if errMsg != "" {
		closeMsg.Error = &errMsg
	}
	return sess.Stream.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_Close{
			Close: closeMsg,
		},
	})
}

func (sess *Session) handleTcpData(data *pb.TcpData) {
	// Data from client (backend response) — write to browser connection
	conn := sess.GetBrowserConn(data.ConnectionId)
	if conn == nil {
		return
	}
	conn.Write(data.Data)
}

func (sess *Session) handleTcpClose(close *pb.TcpClose) {
	// Client closed backend connection — close browser connection
	sess.RemoveBrowserConn(close.ConnectionId)
}

// AddConnection stores a backend TCP connection (client side)
func (sess *Session) AddConnection(connID string, conn net.Conn) {
	sess.backendMu.Lock()
	sess.backendConns[connID] = conn
	sess.backendMu.Unlock()
}

// RemoveConnection removes and closes a backend TCP connection
func (sess *Session) RemoveConnection(connID string) {
	sess.backendMu.Lock()
	conn, ok := sess.backendConns[connID]
	delete(sess.backendConns, connID)
	sess.backendMu.Unlock()
	if ok && conn != nil {
		conn.Close()
	}
}

// AddBrowserConn stores a browser TCP connection (server side)
func (sess *Session) AddBrowserConn(connID string, conn net.Conn) {
	sess.browserMu.Lock()
	sess.browserConns[connID] = conn
	sess.browserMu.Unlock()
}

// RemoveBrowserConn removes and closes a browser TCP connection
func (sess *Session) RemoveBrowserConn(connID string) {
	sess.browserMu.Lock()
	conn, ok := sess.browserConns[connID]
	delete(sess.browserConns, connID)
	sess.browserMu.Unlock()
	if ok && conn != nil {
		conn.Close()
	}
}

// GetBrowserConn returns a browser TCP connection
func (sess *Session) GetBrowserConn(connID string) net.Conn {
	sess.browserMu.Lock()
	defer sess.browserMu.Unlock()
	return sess.browserConns[connID]
}

// StartGRPCServer starts the gRPC server
func StartGRPCServer(addr string, tlsConfig *tls.Config, tunnelServer *Server) (*ggrpc.Server, net.Listener, error) {
	creds := credentials.NewTLS(tlsConfig)
	grpcServer := ggrpc.NewServer(ggrpc.Creds(creds))
	pb.RegisterTunnelServiceServer(grpcServer, tunnelServer)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("gRPC listen: %w", err)
	}

	go func() {
		log.Printf("gRPC server listening on %s", addr)
		if err := grpcServer.Serve(lis); err != nil {
			log.Printf("gRPC server error: %v", err)
		}
	}()

	return grpcServer, lis, nil
}

// StartGRPCServerOnListener starts the gRPC server on an existing listener
func StartGRPCServerOnListener(lis net.Listener, tunnelServer *Server) {
	// We need TLS config for gRPC creds, but the listener already does TLS
	// Use insecure creds since TLS is handled by the listener
	grpcServer := ggrpc.NewServer()
	pb.RegisterTunnelServiceServer(grpcServer, tunnelServer)

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Printf("gRPC server error: %v", err)
		}
	}()
}

// StartGRPCServerOnChan starts the gRPC server accepting connections from a channel
// The connections are already TLS-wrapped, so we use insecure creds
func StartGRPCServerOnChan(connChan chan net.Conn, tlsConfig *tls.Config, tunnelServer *Server) {
	grpcServer := ggrpc.NewServer()
	pb.RegisterTunnelServiceServer(grpcServer, tunnelServer)

	lis := &chanListener{
		connChan: connChan,
		addr:     &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8443},
	}

	if err := grpcServer.Serve(lis); err != nil {
		log.Printf("gRPC server error: %v", err)
	}
}

// chanListener implements net.Listener using a channel of connections
type chanListener struct {
	connChan chan net.Conn
	addr     net.Addr
}

func (l *chanListener) Accept() (net.Conn, error) {
	conn, ok := <-l.connChan
	if !ok {
		return nil, fmt.Errorf("listener closed")
	}
	return conn, nil
}

func (l *chanListener) Close() error {
	return nil
}

func (l *chanListener) Addr() net.Addr {
	return l.addr
}

// Ensure imports
var _ = time.Second