package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	adminSessionCookie = "http_tunnels_admin"
	adminSessionTTL    = 24 * time.Hour
)

func (a *App) authenticatePassword(password string) bool {
	if a.Config.WebPassword == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(password)), []byte(a.Config.WebPassword)) == 1
}

func (a *App) setAdminSession(w http.ResponseWriter) error {
	expiresAt := time.Now().Add(adminSessionTTL).Unix()
	payload := strconv.FormatInt(expiresAt, 10)
	signature := a.signSession(payload)
	value := base64.RawURLEncoding.EncodeToString([]byte(payload + "." + signature))

	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.Config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(expiresAt, 0),
		MaxAge:   int(adminSessionTTL.Seconds()),
	})
	return nil
}

func (a *App) clearAdminSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.Config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func (a *App) isAdminAuthenticated(r *http.Request) bool {
	cookie, err := r.Cookie(adminSessionCookie)
	if err != nil || cookie.Value == "" {
		return false
	}

	decoded, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(decoded), ".", 2)
	if len(parts) != 2 {
		return false
	}
	payload := parts[0]
	signature := parts[1]
	if !hmac.Equal([]byte(signature), []byte(a.signSession(payload))) {
		return false
	}

	expiresAt, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() <= expiresAt
}

func (a *App) signSession(payload string) string {
	mac := hmac.New(sha256.New, []byte(a.Config.SessionSecret))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *App) requireAdminAPI(w http.ResponseWriter, r *http.Request) bool {
	if !a.isAdminAuthenticated(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "authentication required",
		})
		return false
	}
	return true
}

func (a *App) ensureAdminConfigured() error {
	if err := a.Config.ValidateAdminConfiguration(); err != nil {
		return fmt.Errorf("admin authentication is unavailable: %w", err)
	}
	return nil
}
