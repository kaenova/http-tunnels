package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kaenova/http-tunnels/internal/protocol"
)

// App is the main tunnel server application
type App struct {
	config       Config
	store        *Store
	pending      *PendingStore
	assets       fs.FS
	assetHandler http.Handler
	sessions     *TunnelSessionStore
	server       *http.Server
}

// NewApp creates a new tunnel server application
func NewApp(config Config, assets fs.FS) (*App, error) {
	store, err := OpenStore(config.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}

	app := &App{
		config:   config,
		store:    store,
		pending:  NewPendingStore(time.Duration(config.DefaultRequestTimeout) * time.Millisecond),
		assets:   assets,
		sessions: NewTunnelSessionStore(),
	}
	if assets != nil {
		app.assetHandler = http.FileServer(http.FS(assets))
	}
	return app, nil
}

// Serve starts the HTTP server on the given listener
func (a *App) Serve(listener net.Listener) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/ping", a.handlePing)
	mux.HandleFunc("/new_tunnel", a.handleNewTunnel)
	mux.HandleFunc("/tunnel", a.handleTunnelWS)
	mux.HandleFunc("/tunnel-response", a.handleTunnelResponseWS)

	// Admin routes
	mux.HandleFunc("/api/admin/", a.handleAdminAPI)
	mux.HandleFunc("/admin/auth/logout", a.handleAdminLogout)
	mux.HandleFunc("/admin/", a.handleAdminApp)

	// Catch-all for tunnel HTTP proxy
	mux.HandleFunc("/", a.handleTunnelHTTP)

	a.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
	}

	log.Printf("Tunnel server listening on %s", listener.Addr().String())
	return a.server.Serve(listener)
}

// Shutdown gracefully shuts down the server
func (a *App) Shutdown() error {
	a.pending.Stop()
	if a.server != nil {
		return a.server.Close()
	}
	return nil
}

// Close closes the server and store
func (a *App) Close() error {
	a.pending.Stop()
	if a.store != nil {
		a.store.Close()
	}
	return nil
}

// Run starts the server on the configured address
func (a *App) Run() error {
	listener, err := net.Listen("tcp", a.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return a.Serve(listener)
}

// --- Helper functions shared across handlers ---

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func decodeJSONBody[T any](r *http.Request, target *T) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func buildRequestPath(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return r.URL.Path
	}
	return r.URL.Path + "?" + r.URL.RawQuery
}

func requestContext(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	if r.Context() != nil {
		return r.Context()
	}
	return context.Background()
}

func hashValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func normalizeRequestHost(host string) string {
	return protocol.NormalizeHost(host)
}

func parsePaginationParams(r *http.Request) (page int, pageSize int) {
	page = 1
	pageSize = 10
	if value := strings.TrimSpace(r.URL.Query().Get("page")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			page = parsed
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("pageSize")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			pageSize = parsed
		}
	}
	return normalizePagination(page, pageSize)
}

func normalizePagination(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func totalPages(totalItems int64, pageSize int) int {
	if totalItems <= 0 {
		return 1
	}
	return int(math.Ceil(float64(totalItems) / float64(pageSize)))
}

func formatAdminConfigurationError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}

func httpStatusForError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}

func (a *App) logError(context string, err error) {
	if err == nil {
		return
	}
	log.Printf("%s: %v", context, err)
}

func (a *App) serveAsset(w http.ResponseWriter, r *http.Request) {
	if a.assetHandler == nil {
		http.Error(w, "Admin assets are not available", http.StatusServiceUnavailable)
		return
	}
	a.assetHandler.ServeHTTP(w, r)
}

func (a *App) serveAdminIndex(w http.ResponseWriter, r *http.Request) {
	if a.assets == nil {
		http.Error(w, "Admin assets are not available", http.StatusServiceUnavailable)
		return
	}
	content, err := fs.ReadFile(a.assets, "index.html")
	if err != nil {
		http.Error(w, "Admin assets are not available", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func formatBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	} else if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	} else {
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
}