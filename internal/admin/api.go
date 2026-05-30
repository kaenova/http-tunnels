package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/kaenova/http-tunnels/internal/grpc"
)

// Server handles admin API requests
type Server struct {
	tunnelServer *grpc.Server
	adminUser    string
	adminPass    string
	sessions     map[string]time.Time
	mu           sync.RWMutex
}

// NewServer creates a new admin API server
func NewServer(tunnelServer *grpc.Server) *Server {
	adminUser := os.Getenv("ADMIN_USER")
	if adminUser == "" {
		adminUser = "admin"
	}

	adminPass := os.Getenv("ADMIN_PASS")
	if adminPass == "" {
		// Generate random password
		bytes := make([]byte, 4)
		rand.Read(bytes)
		adminPass = hex.EncodeToString(bytes)
		log.Printf("Admin password: %s (set ADMIN_PASS env var to customize)", adminPass)
	}

	return &Server{
		tunnelServer: tunnelServer,
		adminUser:    adminUser,
		adminPass:    adminPass,
		sessions:     make(map[string]time.Time),
	}
}

// Handler returns an HTTP handler for admin API routes
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/admin/auth/session", s.handleSession)
	mux.HandleFunc("/api/admin/auth/login", s.handleLogin)
	mux.HandleFunc("/api/admin/dashboard", s.authMiddleware(s.handleDashboard))
	mux.HandleFunc("/api/admin/tunnels", s.authMiddleware(s.handleTunnels))
	mux.HandleFunc("/api/admin/request-activity", s.authMiddleware(s.handleRequestActivity))

	return mux
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"authenticated": false,
			})
			return
		}

		s.mu.RLock()
		expiry, ok := s.sessions[cookie.Value]
		s.mu.RUnlock()

		if !ok || time.Now().After(expiry) {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"authenticated": false,
			})
			return
		}

		next(w, r)
	}
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"authenticated": false,
			"configured":    true,
		})
		return
	}

	s.mu.RLock()
	expiry, ok := s.sessions[cookie.Value]
	s.mu.RUnlock()

	if !ok || time.Now().After(expiry) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"authenticated": false,
			"configured":    true,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"authenticated": true,
		"configured":    true,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if req.Password != s.adminPass {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid password"})
		return
	}

	// Create session
	bytes := make([]byte, 16)
	rand.Read(bytes)
	sessionID := hex.EncodeToString(bytes)

	s.mu.Lock()
	s.sessions[sessionID] = time.Now().Add(24 * time.Hour)
	s.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:    "session",
		Value:   sessionID,
		Path:    "/",
		Expires: time.Now().Add(24 * time.Hour),
	})

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	sessions := s.tunnelServer.ListSessions()
	activeCount := len(sessions)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"summary": map[string]interface{}{
			"activeTunnels":     activeCount,
			"registeredTunnels": activeCount,
			"totalRequests":     0,
			"dataTransferredBytes": 0,
		},
		"activeTunnels": sessionsToRecords(sessions),
		"recentRequests": []interface{}{},
		"recentTunnelCreates": []interface{}{},
	})
}

func (s *Server) handleTunnels(w http.ResponseWriter, r *http.Request) {
	sessions := s.tunnelServer.ListSessions()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":      sessionsToRecords(sessions),
		"page":       1,
		"pageSize":   50,
		"totalItems": len(sessions),
		"totalPages": 1,
	})
}

func (s *Server) handleRequestActivity(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":      []interface{}{},
		"page":       1,
		"pageSize":   50,
		"totalItems": 0,
		"totalPages": 0,
	})
}

func sessionsToRecords(sessions []*grpc.Session) []map[string]interface{} {
	records := make([]map[string]interface{}, 0, len(sessions))
	for _, sess := range sessions {
		records = append(records, map[string]interface{}{
			"id":          sess.Subdomain,
			"domain":      sess.Subdomain + ".t.kaenova.my.id",
			"state":       "connected",
			"createdAt":   sess.ConnectedAt.Format(time.RFC3339),
			"connectedAt": sess.ConnectedAt.Format(time.RFC3339),
			"requestCount": 0,
		})
	}
	return records
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

var _ = fmt.Sprintf