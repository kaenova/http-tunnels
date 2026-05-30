package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kaenova/http-tunnels/internal/protocol"
)

type App struct {
	config       Config
	store        *Store
	assets       fs.FS
	assetHandler http.Handler
	sessions     sync.Map
}

func NewApp(config Config, assets fs.FS) (*App, error) {
	store, err := OpenStore(config.DBPath)
	if err != nil {
		return nil, err
	}

	app := &App{
		config: config,
		store:  store,
		assets: assets,
	}
	if assets != nil {
		app.assetHandler = http.FileServer(http.FS(assets))
	}
	return app, nil
}

func (a *App) Close() error {
	return a.store.Close()
}

func (a *App) Run() error {
	server := &http.Server{
		Addr:              a.config.ListenAddr,
		Handler:           a,
		ReadHeaderTimeout: 15 * time.Second,
	}
	log.Printf("Listening on %s", a.config.ListenAddr)
	return server.ListenAndServe()
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := normalizeRequestHost(r.Host)
	if session, ok := a.getSession(host); ok {
		a.handleTunnelHTTP(session, w, r)
		return
	}

	switch {
	case r.URL.Path == "/ping":
		a.handlePing(w, r)
	case r.URL.Path == "/new_tunnel":
		a.handleNewTunnel(w, r)
	case r.URL.Path == "/tunnel":
		a.handleTunnelWebSocket(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/admin/"):
		a.handleAdminAPI(w, r)
	case r.URL.Path == "/admin/auth/logout":
		a.handleAdminLogout(w, r)
	case r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/"):
		a.handleAdminApp(w, r)
	case strings.HasPrefix(r.URL.Path, "/assets/") || r.URL.Path == "/favicon.ico":
		a.serveAsset(w, r)
	default:
		http.Error(w, "Tunnel not found", http.StatusNotFound)
	}
}

func (a *App) getSession(host string) (*TunnelSession, bool) {
	value, ok := a.sessions.Load(host)
	if !ok {
		return nil, false
	}
	return value.(*TunnelSession), true
}

func (a *App) setSession(host string, session *TunnelSession) {
	a.sessions.Store(host, session)
}

func (a *App) deleteSession(host string, expected *TunnelSession) {
	value, ok := a.sessions.Load(host)
	if !ok {
		return
	}
	if expected != nil && value != expected {
		return
	}
	a.sessions.Delete(host)
}

func (a *App) replaceSession(host string, session *TunnelSession) (previous *TunnelSession) {
	value, loaded := a.sessions.LoadOrStore(host, session)
	if !loaded {
		return nil
	}
	previous = value.(*TunnelSession)
	a.sessions.Store(host, session)
	return previous
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

func hashValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func normalizeRequestHost(host string) string {
	return protocol.NormalizeHost(host)
}

func randomSubdomain() string {
	return strings.ToLower(protocol.GenerateID(8))
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

func requestContext(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	if r.Context() != nil {
		return r.Context()
	}
	return context.Background()
}

func (a *App) logError(context string, err error) {
	if err == nil {
		return
	}
	log.Printf("%s: %v", context, err)
}

func (a *App) verbose(format string, v ...any) {
	if a.config.Verbose {
		log.Printf("[verbose] "+format, v...)
	}
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

func formatAdminConfigurationError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
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
