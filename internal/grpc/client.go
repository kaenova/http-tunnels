package grpc

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	pb "github.com/kaenova/http-tunnels/internal/grpc/pb"
	"github.com/kaenova/http-tunnels/internal/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Client manages the tunnel connection to the server
type Client struct {
	conn        *grpc.ClientConn
	stream      pb.TunnelService_TunnelClient
	subdomain   string
	backendHost string
	backendPort int32
	connections map[string]net.Conn
	connMu      sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewClient creates a new tunnel client
func NewClient() *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		connections: make(map[string]net.Conn),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Connect connects to the tunnel server and registers
func (c *Client) Connect(addr string, useTLS bool, subdomain, backendHost string, backendPort int32) error {
	var opts []grpc.DialOption

	if useTLS {
		creds := credentials.NewTLS(tls.ClientTLSConfig())
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.Dial(addr, opts...)
	if err != nil {
		return fmt.Errorf("dialing server: %w", err)
	}
	c.conn = conn

	client := pb.NewTunnelServiceClient(conn)
	stream, err := client.Tunnel(c.ctx)
	if err != nil {
		conn.Close()
		return fmt.Errorf("opening tunnel stream: %w", err)
	}
	c.stream = stream

	// Send register request
	err = stream.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_Register{
			Register: &pb.RegisterRequest{
				Subdomain:   subdomain,
				BackendHost: backendHost,
				BackendPort: backendPort,
			},
		},
	})
	if err != nil {
		conn.Close()
		return fmt.Errorf("sending register: %w", err)
	}

	// Wait for ack
	msg, err := stream.Recv()
	if err != nil {
		conn.Close()
		return fmt.Errorf("receiving register ack: %w", err)
	}

	ack := msg.GetRegisterAck()
	if ack == nil || !ack.Success {
		conn.Close()
		return fmt.Errorf("registration failed: %s", ack.GetError())
	}

	c.subdomain = ack.AssignedSubdomain
	c.backendHost = backendHost
	c.backendPort = backendPort

	log.Printf("Connected to tunnel server at %s, subdomain=%s", addr, c.subdomain)
	return nil
}

// Run starts the client stream loop
func (c *Client) Run() error {
	for {
		msg, err := c.stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream receive error: %w", err)
		}

		switch payload := msg.Payload.(type) {
		case *pb.TunnelMessage_Open:
			c.handleTcpOpen(payload.Open)
		case *pb.TunnelMessage_Data:
			c.handleTcpData(payload.Data)
		case *pb.TunnelMessage_Close:
			c.handleTcpClose(payload.Close)
		case *pb.TunnelMessage_Ping:
			c.sendPong()
		}
	}
}

func (c *Client) handleTcpOpen(open *pb.TcpOpen) {
	connID := open.ConnectionId
	addr := fmt.Sprintf("%s:%d", c.backendHost, c.backendPort)

	backend, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		log.Printf("backend dial error (conn=%s): %v", connID, err)
		c.sendTcpClose(connID, fmt.Sprintf("backend dial failed: %v", err))
		return
	}

	c.connMu.Lock()
	c.connections[connID] = backend
	c.connMu.Unlock()

	// Start reading from backend and sending to server
	go c.forwardBackendToServer(connID, backend)
}

func (c *Client) forwardBackendToServer(connID string, backend net.Conn) {
	defer func() {
		c.connMu.Lock()
		delete(c.connections, connID)
		c.connMu.Unlock()
		backend.Close()
	}()

	buf := make([]byte, 32768)
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		backend.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := backend.Read(buf)
		if err != nil {
			if err != io.EOF && !isNetTimeout(err) {
				c.sendTcpClose(connID, fmt.Sprintf("backend read error: %v", err))
			} else {
				c.sendTcpClose(connID, "")
			}
			return
		}

		// Copy data to avoid race
		data := make([]byte, n)
		copy(data, buf[:n])

		err = c.stream.Send(&pb.TunnelMessage{
			Payload: &pb.TunnelMessage_Data{
				Data: &pb.TcpData{
					ConnectionId: connID,
					Data:         data,
				},
			},
		})
		if err != nil {
			return
		}
	}
}

func (c *Client) handleTcpData(data *pb.TcpData) {
	c.connMu.Lock()
	conn, ok := c.connections[data.ConnectionId]
	c.connMu.Unlock()

	if ok && conn != nil {
		conn.Write(data.Data)
	}
}

func (c *Client) handleTcpClose(close *pb.TcpClose) {
	c.connMu.Lock()
	conn, ok := c.connections[close.ConnectionId]
	delete(c.connections, close.ConnectionId)
	c.connMu.Unlock()

	if ok && conn != nil {
		conn.Close()
	}
}

func (c *Client) sendTcpClose(connID string, errMsg string) {
	closeMsg := &pb.TcpClose{
		ConnectionId: connID,
	}
	if errMsg != "" {
		closeMsg.Error = &errMsg
	}
	c.stream.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_Close{
			Close: closeMsg,
		},
	})
}

func (c *Client) sendPong() {
	c.stream.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_Pong{
			Pong: &pb.Pong{},
		},
	})
}

// Close disconnects from the server
func (c *Client) Close() {
	c.cancel()
	if c.conn != nil {
		c.conn.Close()
	}
}

// Subdomain returns the assigned subdomain
func (c *Client) Subdomain() string {
	return c.subdomain
}

func isNetTimeout(err error) bool {
	if err == nil {
		return false
	}
	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout()
	}
	return false
}

// Ensure imports
var _ = time.Second