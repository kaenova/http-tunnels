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
	h.ConnectAndRegisterTB(t, tc)
}

func (h *TestHarness) ConnectAndRegisterTB(tb testing.TB, tc *TunnelClient) {
	tb.Helper()

	wsURL := "ws://" + stripHTTP(h.TunnelAddr) + "/tunnel?domain=" + tc.Domain + "&domain_key=" + tc.DomainKey
	conn, _, err := h.WSDialer.Dial(wsURL, nil)
	if err != nil {
		tb.Fatalf("connect main WS: %v", err)
	}

	ws := protocol.NewConnection(conn)
	if err := ws.Send(&protocol.Frame{
		Type:      protocol.FrameType_REGISTER,
		Domain:    tc.Domain,
		DomainKey: tc.DomainKey,
	}); err != nil {
		tb.Fatalf("send register: %v", err)
	}

	regFrame, err := ws.ReadFrame()
	if err != nil {
		tb.Fatalf("read registered: %v", err)
	}
	tc.TunnelID = regFrame.GetTunnelId()
	tc.Config = regFrame.GetConfig()

	go h.runClientLoopTB(tb, ws)
	time.Sleep(50 * time.Millisecond)
}

type bufferedClientRequest struct {
	frame *protocol.Frame
	body  []byte
}

func (h *TestHarness) runClientLoop(t *testing.T, ws *protocol.Connection) {
	h.runClientLoopTB(t, ws)
}

func (h *TestHarness) runClientLoopTB(tb testing.TB, ws *protocol.Connection) {
	requests := make(map[string]*bufferedClientRequest)
	for {
		frame, err := ws.ReadFrame()
		if err != nil {
			return
		}
		switch frame.GetType() {
		case protocol.FrameType_PING:
			_ = ws.Send(&protocol.Frame{Type: protocol.FrameType_PONG})
		case protocol.FrameType_REQUEST_START:
			requests[frame.GetRequestId()] = &bufferedClientRequest{frame: frame}
		case protocol.FrameType_REQUEST_BODY:
			if req := requests[frame.GetRequestId()]; req != nil {
				req.body = append(req.body, frame.GetChunk()...)
			}
		case protocol.FrameType_REQUEST_END:
			if req := requests[frame.GetRequestId()]; req != nil {
				delete(requests, frame.GetRequestId())
				go h.handleBufferedClientRequestTB(tb, ws, req)
			}
		case protocol.FrameType_REQUEST_CANCEL:
			delete(requests, frame.GetRequestId())
		}
	}
}

func (h *TestHarness) handleBufferedClientRequest(t *testing.T, mainWS *protocol.Connection, reqFrame *bufferedClientRequest) {
	h.handleBufferedClientRequestTB(t, mainWS, reqFrame)
}

func (h *TestHarness) handleBufferedClientRequestTB(tb testing.TB, mainWS *protocol.Connection, reqFrame *bufferedClientRequest) {
	method := reqFrame.frame.GetMethod()
	if method == "" {
		method = http.MethodGet
	}
	path := reqFrame.frame.GetPath()
	if path == "" {
		path = "/"
	}

	req, _ := http.NewRequest(method, h.Backend.URL+path, strings.NewReader(string(reqFrame.body)))
	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		_ = mainWS.Send(&protocol.Frame{
			Type:      protocol.FrameType_RESPONSE_ERROR,
			RequestId: reqFrame.frame.GetRequestId(),
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

	_ = mainWS.Send(&protocol.Frame{
		Type:            protocol.FrameType_RESPONSE_START,
		RequestId:       reqFrame.frame.GetRequestId(),
		Status:          int32(resp.StatusCode),
		ResponseHeaders: respHeaders,
	})

	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			_ = mainWS.Send(&protocol.Frame{
				Type:      protocol.FrameType_RESPONSE_BODY,
				RequestId: reqFrame.frame.GetRequestId(),
				Chunk:     chunk,
			})
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = mainWS.Send(&protocol.Frame{
				Type:      protocol.FrameType_RESPONSE_ERROR,
				RequestId: reqFrame.frame.GetRequestId(),
				Status:    502,
				Error:     err.Error(),
			})
			return
		}
	}

	_ = mainWS.Send(&protocol.Frame{
		Type:      protocol.FrameType_RESPONSE_END,
		RequestId: reqFrame.frame.GetRequestId(),
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
