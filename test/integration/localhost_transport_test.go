package integration

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kaenova/http-tunnels/internal/client"
	"github.com/kaenova/http-tunnels/internal/protocol"
	"github.com/kaenova/http-tunnels/internal/server"
)

func TestHTTP2TunnelWithLocalhostDomains(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "backend-ok "+r.Host+" "+r.URL.Path)
	}))
	defer backend.Close()

	config := server.Config{
		DBPath:                       ":memory:",
		TunnelDomain:                 "localhost",
		WebPassword:                  "test-password",
		SessionSecret:                "test-secret",
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
		t.Fatalf("creating tunnel server: %v", err)
	}
	defer func() { _ = app.Close() }()

	tunnelServer := httptest.NewUnstartedServer(app.Handler())
	tunnelServer.EnableHTTP2 = true
	tunnelServer.StartTLS()
	defer tunnelServer.Close()

	tlsHTTPClient := tunnelServer.Client()
	tlsHTTPClient.Timeout = 15 * time.Second
	transport := tlsHTTPClient.Transport.(*http.Transport)

	subdomain := "probe-local"
	domain := subdomain + ".localhost"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.Run(ctx, client.Options{
			Host:               tunnelServer.URL,
			BackendURL:         backend.URL,
			Subdomain:          subdomain,
			PreferredTransport: protocol.TransportHTTP2,
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

	waitForTunnelReady(t, tlsHTTPClient, tunnelServer.URL, domain)

	resp := hostRequest(t, tlsHTTPClient, tunnelServer.URL, domain, "/through-localhost")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("tunnel response status=%d body=%s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "backend-ok") {
		t.Fatalf("expected backend response, got %q", string(body))
	}

	adminClient := *tlsHTTPClient
	adminClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	adminResp := hostRequest(t, &adminClient, tunnelServer.URL, "localhost", "/")
	defer adminResp.Body.Close()
	if adminResp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(adminResp.Body)
		t.Fatalf("admin root status=%d body=%s", adminResp.StatusCode, string(body))
	}
	if location := adminResp.Header.Get("Location"); location != "/admin/auth/login" {
		t.Fatalf("admin root redirect location=%q", location)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("client.Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for tunnel client shutdown")
	}
}

func waitForTunnelReady(t *testing.T, httpClient *http.Client, baseURL, host string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := hostRequestNoFail(httpClient, baseURL, host, "/__ready")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("tunnel did not become ready for host %s", host)
}

func hostRequest(t *testing.T, httpClient *http.Client, baseURL, host, path string) *http.Response {
	t.Helper()
	resp, err := hostRequestNoFail(httpClient, baseURL, host, path)
	if err != nil {
		t.Fatalf("request host=%s path=%s: %v", host, path, err)
	}
	return resp
}

func hostRequestNoFail(httpClient *http.Client, baseURL, host, path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Host = host
	return httpClient.Do(req)
}
