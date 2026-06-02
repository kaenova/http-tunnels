package server

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

type loginRequest struct {
	Password string `json:"password"`
}

func (a *App) handleAdminApp(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminHost(r) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	isLoginRoute := r.URL.Path == "/admin/auth/login"
	isAuthenticated := a.isAdminAuthenticated(r)

	if isLoginRoute {
		if isAuthenticated {
			http.Redirect(w, r, "/admin", http.StatusFound)
			return
		}
		a.serveAdminIndex(w, r)
		return
	}

	if !isAuthenticated {
		http.Redirect(w, r, "/admin/auth/login", http.StatusFound)
		return
	}

	a.serveAdminIndex(w, r)
}

func (a *App) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminHost(r) {
		http.NotFound(w, r)
		return
	}
	a.clearAdminSession(w)
	http.Redirect(w, r, "/admin/auth/login", http.StatusFound)
}

func (a *App) handleAdminAPI(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminHost(r) {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/admin")
	switch {
	case path == "/auth/session":
		a.handleAdminSessionAPI(w, r)
	case path == "/auth/login":
		a.handleAdminLoginAPI(w, r)
	case path == "/dashboard":
		if !a.requireAdminAPI(w, r) {
			return
		}
		a.handleDashboardAPI(w, r)
	case path == "/request-activity":
		if !a.requireAdminAPI(w, r) {
			return
		}
		a.handleRequestActivityListAPI(w, r)
	case strings.HasPrefix(path, "/request-activity/"):
		if !a.requireAdminAPI(w, r) {
			return
		}
		a.handleRequestActivityDetailAPI(w, r, strings.TrimPrefix(path, "/request-activity/"))
	case path == "/tunnels":
		if !a.requireAdminAPI(w, r) {
			return
		}
		a.handleTunnelListAPI(w, r)
	case strings.HasPrefix(path, "/tunnels/"):
		if !a.requireAdminAPI(w, r) {
			return
		}
		a.handleTunnelDetailAPI(w, r, strings.TrimPrefix(path, "/tunnels/"))
	default:
		http.NotFound(w, r)
	}
}

func (a *App) handleAdminSessionAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := a.ensureAdminConfigured()
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": a.isAdminAuthenticated(r),
		"configured":    err == nil,
		"message":       formatAdminConfigurationError(err),
	})
}

func (a *App) handleAdminLoginAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := a.ensureAdminConfigured(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "admin authentication is not configured",
			"message": err.Error(),
		})
		return
	}

	var payload loginRequest
	if err := decodeJSONBody(r, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid login payload",
		})
		return
	}

	if !a.authenticatePassword(payload.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "invalid password",
		})
		return
	}

	if err := a.setAdminSession(w); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "could not create admin session",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
	})
}

func (a *App) handleDashboardAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response, err := a.store.GetDashboard(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	response.Summary.ActiveTraffic = int64(a.sessions.ActiveRequestCount())
	writeJSON(w, http.StatusOK, response)
}

func (a *App) handleRequestActivityListAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	page, pageSize := parsePaginationParams(r)
	filters := RequestLogFilters{
		Search:      strings.TrimSpace(r.URL.Query().Get("search")),
		Subdomain:   strings.TrimSpace(r.URL.Query().Get("subdomain")),
		Method:      strings.TrimSpace(r.URL.Query().Get("method")),
		StatusClass: strings.TrimSpace(r.URL.Query().Get("statusClass")),
	}

	response, err := a.store.ListAllRequestLogs(r.Context(), filters, page, pageSize)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) handleRequestActivityDetailAPI(w http.ResponseWriter, r *http.Request, suffix string) {
	requestLogID := strings.Trim(strings.TrimSpace(suffix), "/")
	if requestLogID == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response, err := a.store.GetRequestLogByID(r.Context(), requestLogID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) handleTunnelListAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, pageSize := parsePaginationParams(r)
	response, err := a.store.ListActiveTunnels(r.Context(), page, pageSize)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) handleTunnelDetailAPI(w http.ResponseWriter, r *http.Request, suffix string) {
	tunnelID := strings.Trim(strings.TrimSpace(suffix), "/")
	if tunnelID == "" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		page, pageSize := parsePaginationParams(r)
		response, err := a.store.GetTunnelDetail(r.Context(), tunnelID, page, pageSize)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, response)
	case http.MethodDelete:
		if err := a.store.DeleteTunnel(r.Context(), tunnelID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		a.closeTunnelSession(tunnelID)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) closeTunnelSession(tunnelID string) {
	var targetHost string
	var targetSession *TunnelSession
	for domain, session := range a.sessions.GetAll() {
		if session.TunnelID == tunnelID {
			targetHost = domain
			targetSession = session
			break
		}
	}
	if targetSession == nil {
		return
	}
	a.sessions.Delete(targetHost)
	targetSession.Conn.Close()
}
