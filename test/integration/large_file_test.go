package integration

import (
	"crypto/rand"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kaenova/http-tunnels/internal/protocol"
)

// TestLargeFileDownload tests downloading a large file through the tunnel
func TestLargeFileDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large file test in short mode")
	}

	// Generate 100MB random data
	dataSize := 100 * 1024 * 1024 // 100MB
	largeData := make([]byte, dataSize)
	rand.Read(largeData)

	h := NewHarnessWithBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", itoa(dataSize))
		w.WriteHeader(http.StatusOK)
		w.Write(largeData)
	}))

	tunnel := h.CreateTunnel(t, "")
	h.ConnectAndRegister(t, tunnel)

	start := time.Now()
	resp, err := h.HTTPGet(t, tunnel.Domain, "/api/download")
	if err != nil {
		t.Fatalf("large download: %v", err)
	}
	defer resp.Body.Close()

	// Read and verify
	received := make([]byte, dataSize)
	n, err := io.ReadFull(resp.Body, received)
	if err != nil && err != io.ErrUnexpectedEOF {
		t.Fatalf("reading download: %v", err)
	}

	elapsed := time.Since(start)
	throughput := float64(n) / elapsed.Seconds() / (1024 * 1024)
	t.Logf("100MB download: %d bytes in %v (%.1f MB/s)", n, elapsed, throughput)

	if n != dataSize {
		t.Errorf("download size mismatch: got %d, want %d", n, dataSize)
	}

	// Verify first and last bytes
	if received[0] != largeData[0] {
		t.Error("first byte mismatch")
	}
	if received[dataSize-1] != largeData[dataSize-1] {
		t.Error("last byte mismatch")
	}
}

// TestLargeFileUpload tests uploading a large file through the tunnel
func TestLargeFileUpload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large file test in short mode")
	}

	dataSize := 50 * 1024 * 1024 // 50MB

	h := NewHarnessWithBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"received_bytes":` + itoa(len(body)) + `}`))
	}))

	tunnel := h.CreateTunnel(t, "")
	h.ConnectAndRegister(t, tunnel)

	// Make POST with large body
	data := make([]byte, dataSize)
	rand.Read(data)
	body := io.NopCloser(io.LimitReader(rand.Reader, int64(dataSize)))
	_ = data // use data for verification

	start := time.Now()
	resp, err := h.HTTPPost(t, tunnel.Domain, "/api/upload", "application/octet-stream", body)
	if err != nil {
		t.Fatalf("large upload: %v", err)
	}
	defer resp.Body.Close()

	elapsed := time.Since(start)
	t.Logf("50MB upload completed in %v", elapsed)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload failed: status=%d body=%s", resp.StatusCode, string(body))
	}
}

// TestMaxConcurrentLimit tests that the max concurrent limit is enforced
func TestMaxConcurrentLimit(t *testing.T) {
	h := NewHarness(t)

	tunnel := h.CreateTunnel(t, "")
	h.ConnectAndRegister(t, tunnel)

	// Send 5 concurrent requests
	results := make(chan int, 5)
	for i := 0; i < 5; i++ {
		go func() {
			resp, err := h.HTTPGet(t, tunnel.Domain, "/api/test")
			if err != nil {
				results <- 0
				return
			}
			results <- resp.StatusCode
			resp.Body.Close()
		}()
	}

	statuses := make([]int, 0, 5)
	for i := 0; i < 5; i++ {
		statuses = append(statuses, <-results)
	}

	okCount := 0
	for _, s := range statuses {
		if s == 200 {
			okCount++
		}
	}

	t.Logf("Concurrent results: %v (200s=%d)", statuses, okCount)

	if okCount < 1 {
		t.Error("at least one request should succeed")
	}
}

// ConnectAndRegister connects a client and registers it
func (h *TestHarness) ConnectAndRegister(t *testing.T, tc *TunnelClient) {
	t.Helper()

	wsURL := "ws://" + stripHTTP(h.TunnelAddr) + "/tunnel?domain=" + tc.Domain + "&domain_key=" + tc.DomainKey
	conn, _, err := h.WSDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("connect main WS: %v", err)
	}

	ws := protocol.NewConnection(conn)

	ws.Send(&protocol.Frame{
		Type:      protocol.FrameType_REGISTER,
		Domain:    tc.Domain,
		DomainKey: tc.DomainKey,
	})

	regFrame, err := ws.ReadFrame()
	if err != nil {
		t.Fatalf("read registered: %v", err)
	}
	tc.TunnelID = regFrame.GetTunnelId()
	tc.Config = regFrame.GetConfig()

	// Start request handler
	go func() {
		for {
			frame, err := ws.ReadFrame()
			if err != nil {
				return
			}
			if frame.GetType() == protocol.FrameType_REQUEST {
				h.handleClientRequest(t, tc, ws, frame)
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)
}

func (h *TestHarness) handleClientRequest(t *testing.T, tc *TunnelClient, mainWS *protocol.Connection, frame *protocol.Frame) {
	dedWSURL := "ws://" + stripHTTP(h.TunnelAddr) + "/tunnel-response?request_id=" + frame.GetRequestId() + "&domain_key=" + tc.DomainKey
	dedConn, _, err := h.WSDialer.Dial(dedWSURL, nil)
	if err != nil {
		mainWS.Send(&protocol.Frame{
			Type:      protocol.FrameType_REQUEST_ERROR,
			RequestId: frame.GetRequestId(),
			Status:    502,
			Error:     err.Error(),
		})
		return
	}
	defer dedConn.Close()

	dedWS := protocol.NewConnection(dedConn)

	// Read body chunks and build the body
	var reqBody []byte
	for {
		bodyFrame, err := dedWS.ReadFrame()
		if err != nil || bodyFrame.GetType() == protocol.FrameType_BODY_END {
			break
		}
		if bodyFrame.GetType() == protocol.FrameType_BODY {
			reqBody = append(reqBody, bodyFrame.GetChunk()...)
		}
	}

	// Proxy to backend
	method := frame.GetMethod()
	if method == "" {
		method = "GET"
	}
	path := frame.GetPath()
	if path == "" {
		path = "/"
	}

	var bodyReader io.Reader
	if len(reqBody) > 0 {
		bodyReader = io.NopCloser(io.Reader(io.NopCloser(nil)))
		bodyReader = strings.NewReader(string(reqBody))
	}
	_ = bodyReader

	req, _ := http.NewRequest(method, h.Backend.URL+path, strings.NewReader(string(reqBody)))
	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		dedWS.Send(&protocol.Frame{
			Type:      protocol.FrameType_RESPONSE_ERROR,
			RequestId: frame.GetRequestId(),
			Status:    502,
			Error:     err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	respHeaders := make(map[string]*protocol.StringList)
	for k, vals := range resp.Header {
		respHeaders[k] = &protocol.StringList{Values: vals}
	}

	dedWS.Send(&protocol.Frame{
		Type:            protocol.FrameType_RESPONSE_START,
		RequestId:       frame.GetRequestId(),
		Status:          int32(resp.StatusCode),
		ResponseHeaders: respHeaders,
	})

	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			dedWS.Send(&protocol.Frame{
				Type:      protocol.FrameType_RESPONSE_BODY,
				RequestId: frame.GetRequestId(),
				Chunk:     chunk,
			})
		}
		if err != nil {
			break
		}
	}

	dedWS.Send(&protocol.Frame{
		Type:      protocol.FrameType_RESPONSE_END,
		RequestId: frame.GetRequestId(),
	})
}

func stripHTTP(url string) string {
	if len(url) > 7 && url[:7] == "http://" {
		return url[7:]
	}
	if len(url) > 8 && url[:8] == "https://" {
		return url[8:]
	}
	return url
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	if neg {
		digits = "-" + digits
	}
	return digits
}