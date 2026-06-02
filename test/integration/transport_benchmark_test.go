package integration

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kaenova/http-tunnels/internal/client"
	"github.com/kaenova/http-tunnels/internal/server"
)

type transportBenchmarkEnv struct {
	backend    *httptest.Server
	server     *httptest.Server
	app        *server.App
	httpClient *http.Client
	domain     string
	cancel     context.CancelFunc
	errCh      chan error
}

func BenchmarkTransportWebSocketLargeDownloadWithSpikeTraffic(b *testing.B) {
	benchmarkTransportLargeDownloadWithSpikeTraffic(b, "websocket")
}

func BenchmarkTransportHTTP2LargeDownloadWithSpikeTraffic(b *testing.B) {
	benchmarkTransportLargeDownloadWithSpikeTraffic(b, "http2")
}

func BenchmarkTransportWebSocketParallelLargeDownloadsWithSpikeTraffic(b *testing.B) {
	benchmarkTransportParallelLargeDownloadsWithSpikeTraffic(b, "websocket")
}

func BenchmarkTransportHTTP2ParallelLargeDownloadsWithSpikeTraffic(b *testing.B) {
	benchmarkTransportParallelLargeDownloadsWithSpikeTraffic(b, "http2")
}

func benchmarkTransportLargeDownloadWithSpikeTraffic(b *testing.B, transport string) {
	const (
		dataSize          = 64 * 1024 * 1024
		spikePerIteration = 24
	)
	largeData := make([]byte, dataSize)

	env := newTransportBenchmarkEnv(b, transport, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__ready":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case "/big":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", itoa(len(largeData)))
			_, _ = w.Write(largeData)
		case "/small":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer env.Close()

	latencies := make([]time.Duration, 0, b.N*spikePerIteration)
	var latMu sync.Mutex

	b.SetBytes(int64(dataSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := env.get("/big")
			if err != nil {
				b.Errorf("big download request failed: %v", err)
				return
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				b.Errorf("big download read failed: %v", err)
			}
		}()

		time.Sleep(25 * time.Millisecond)

		for j := 0; j < spikePerIteration; j++ {
			start := time.Now()
			resp, err := env.get("/small")
			if err != nil {
				b.Fatalf("small spike request failed: %v", err)
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				b.Fatalf("small spike read failed: %v", err)
			}
			latMu.Lock()
			latencies = append(latencies, time.Since(start))
			latMu.Unlock()
		}

		wg.Wait()
	}
	b.StopTimer()
	reportLatencyMetrics(b, transport+"_small", latencies)
}

func benchmarkTransportParallelLargeDownloadsWithSpikeTraffic(b *testing.B, transport string) {
	const (
		dataSize          = 32 * 1024 * 1024
		parallelLarge     = 4
		spikePerIteration = 32
	)
	largeData := make([]byte, dataSize)

	env := newTransportBenchmarkEnv(b, transport, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__ready":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case "/big":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", itoa(len(largeData)))
			_, _ = w.Write(largeData)
		case "/small":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer env.Close()

	latencies := make([]time.Duration, 0, b.N*spikePerIteration)
	var latMu sync.Mutex

	b.SetBytes(int64(parallelLarge * dataSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		wg.Add(parallelLarge)
		for j := 0; j < parallelLarge; j++ {
			go func() {
				defer wg.Done()
				resp, err := env.get("/big")
				if err != nil {
					b.Errorf("parallel big download request failed: %v", err)
					return
				}
				_, err = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if err != nil {
					b.Errorf("parallel big download read failed: %v", err)
				}
			}()
		}

		time.Sleep(25 * time.Millisecond)

		for j := 0; j < spikePerIteration; j++ {
			start := time.Now()
			resp, err := env.get("/small")
			if err != nil {
				b.Fatalf("small spike request failed: %v", err)
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				b.Fatalf("small spike read failed: %v", err)
			}
			latMu.Lock()
			latencies = append(latencies, time.Since(start))
			latMu.Unlock()
		}

		wg.Wait()
	}
	b.StopTimer()
	reportLatencyMetrics(b, transport+"_small", latencies)
}

func newTransportBenchmarkEnv(tb testing.TB, preferredTransport string, backendHandler http.Handler) *transportBenchmarkEnv {
	tb.Helper()

	backend := httptest.NewServer(backendHandler)
	config := server.Config{
		ListenAddr:                   "127.0.0.1:0",
		DBPath:                       ":memory:",
		WebPassword:                  "test-password",
		SessionSecret:                "test-secret",
		TunnelDomain:                 "example.test",
		MaxConcurrentRequests:        500,
		DefaultRequestTimeout:        30000,
		DefaultBackendTimeout:        120000,
		DefaultReconnectEnabled:      true,
		DefaultReconnectInitialDelay: 1000,
		DefaultReconnectMaxDelay:     60000,
		DefaultReconnectMultiplier:   2.0,
		DefaultReconnectMaxRetries:   0,
	}
	app, err := server.NewApp(config, nil)
	if err != nil {
		tb.Fatalf("creating tunnel server: %v", err)
	}

	tunnelServer := httptest.NewUnstartedServer(app.Handler())
	tunnelServer.EnableHTTP2 = true
	tunnelServer.StartTLS()

	tlsHTTPClient := tunnelServer.Client()
	tlsHTTPClient.Timeout = 120 * time.Second
	transport := tlsHTTPClient.Transport.(*http.Transport)

	subdomain := fmt.Sprintf("bench-%s", preferredTransport)
	domain := subdomain + ".example.test"
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- client.Run(ctx, client.Options{
			Host:               tunnelServer.URL,
			BackendURL:         backend.URL,
			Subdomain:          subdomain,
			PreferredTransport: preferredTransport,
			HTTPClient:         tlsHTTPClient,
			WSDialer: &websocket.Dialer{
				EnableCompression: true,
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
					ServerName:         transport.TLSClientConfig.ServerName,
				},
			},
		})
	}()

	env := &transportBenchmarkEnv{
		backend:    backend,
		server:     tunnelServer,
		app:        app,
		httpClient: tlsHTTPClient,
		domain:     domain,
		cancel:     cancel,
		errCh:      errCh,
	}
	env.waitUntilReady(tb)
	return env
}

func (e *transportBenchmarkEnv) waitUntilReady(tb testing.TB) {
	tb.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := e.get("/__ready")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	tb.Fatalf("transport benchmark tunnel did not become ready for domain %s", e.domain)
}

func (e *transportBenchmarkEnv) get(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, e.server.URL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Host = e.domain
	return e.httpClient.Do(req)
}

func (e *transportBenchmarkEnv) Close() {
	if e == nil {
		return
	}
	if e.cancel != nil {
		e.cancel()
	}
	if e.errCh != nil {
		select {
		case <-e.errCh:
		case <-time.After(5 * time.Second):
		}
	}
	if e.server != nil {
		e.server.Close()
	}
	if e.app != nil {
		_ = e.app.Close()
	}
	if e.backend != nil {
		e.backend.Close()
	}
}
