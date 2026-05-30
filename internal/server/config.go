package server

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	ListenAddr    string
	DBPath        string
	ServerMessage string
	WebPassword   string
	SessionSecret string
	CookieSecure  bool
	Verbose       bool
}

func LoadConfig() Config {
	listenAddr := strings.TrimSpace(os.Getenv("LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = ":80"
	}

	dbPath := strings.TrimSpace(os.Getenv("DB_PATH"))
	if dbPath == "" {
		dbPath = "http-tunnels.db"
	}

	cookieSecure := false
	if value := strings.TrimSpace(strings.ToLower(os.Getenv("COOKIE_SECURE"))); value == "1" || value == "true" || value == "yes" {
		cookieSecure = true
	}

	sessionSecret := strings.TrimSpace(os.Getenv("WEB_SESSION_SECRET"))
	if sessionSecret == "" {
		sessionSecret = strings.TrimSpace(os.Getenv("WEB_PASSWORD"))
	}

	verbose := false
	if value := strings.TrimSpace(strings.ToLower(os.Getenv("VERBOSE"))); value == "1" || value == "true" || value == "yes" {
		verbose = true
	}

	return Config{
		ListenAddr:    listenAddr,
		DBPath:        dbPath,
		ServerMessage: strings.TrimSpace(os.Getenv("SERVER_MESSAGE")),
		WebPassword:   strings.TrimSpace(os.Getenv("WEB_PASSWORD")),
		SessionSecret: sessionSecret,
		CookieSecure:  cookieSecure,
		Verbose:       verbose,
	}
}

func (c Config) ValidateAdminConfiguration() error {
	if c.WebPassword == "" {
		return fmt.Errorf("WEB_PASSWORD is not configured")
	}
	if c.SessionSecret == "" {
		return fmt.Errorf("WEB_SESSION_SECRET is not configured")
	}
	return nil
}
