package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminHostRootRedirectsToLogin(t *testing.T) {
	app, err := NewApp(Config{
		ListenAddr:                   "127.0.0.1:0",
		DBPath:                       ":memory:",
		WebPassword:                  "test-password",
		SessionSecret:                "test-secret",
		TunnelDomain:                 "example.test",
		MaxConcurrentRequests:        100,
		DefaultRequestTimeout:        10000,
		DefaultBackendTimeout:        30000,
		DefaultReconnectEnabled:      true,
		DefaultReconnectInitialDelay: 1000,
		DefaultReconnectMaxDelay:     60000,
		DefaultReconnectMultiplier:   2.0,
		DefaultReconnectMaxRetries:   0,
	}, fs.FS(nil))
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	defer app.Close()

	req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	req.Host = "example.test"
	rec := httptest.NewRecorder()

	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected %d, got %d", http.StatusFound, rec.Code)
	}
	if location := rec.Header().Get("Location"); location != "/admin/auth/login" {
		t.Fatalf("expected redirect to /admin/auth/login, got %q", location)
	}
}
