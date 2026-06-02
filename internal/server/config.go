package server

import (
	"os"
	"strconv"
	"strings"
)

// Config holds server configuration
type Config struct {
	ListenAddr                   string
	DBPath                       string
	ServerMessage                string
	WebPassword                  string
	SessionSecret                string
	CookieSecure                 bool
	TLSCertPath                  string
	TLSKeyPath                   string
	MaxConcurrentRequests        int
	DefaultRequestTimeout        int
	DefaultBackendTimeout        int
	DefaultReconnectEnabled      bool
	DefaultReconnectInitialDelay int
	DefaultReconnectMaxDelay     int
	DefaultReconnectMultiplier   float64
	DefaultReconnectMaxRetries   int
	TunnelDomain                 string
}

// LoadConfig loads configuration from environment variables
func LoadConfig() Config {
	tunnelDomain := os.Getenv("TUNNEL_DOMAIN")
	if tunnelDomain == "" {
		tunnelDomain = "localhost"
	}

	return Config{
		ListenAddr:                   getEnv("LISTEN_ADDR", ":8443"),
		DBPath:                       getEnv("DB_PATH", "http-tunnels.db"),
		TunnelDomain:                 tunnelDomain,
		MaxConcurrentRequests:        getEnvInt("MAX_CONCURRENT_REQUESTS", 500),
		DefaultRequestTimeout:        getEnvInt("DEFAULT_REQUEST_TIMEOUT", 10000),
		DefaultBackendTimeout:        getEnvInt("DEFAULT_BACKEND_TIMEOUT", 30000),
		DefaultReconnectEnabled:      getEnvBool("DEFAULT_RECONNECT_ENABLED", true),
		DefaultReconnectInitialDelay: getEnvInt("DEFAULT_RECONNECT_INITIAL_DELAY", 1000),
		DefaultReconnectMaxDelay:     getEnvInt("DEFAULT_RECONNECT_MAX_DELAY", 60000),
		DefaultReconnectMultiplier:   getEnvFloat("DEFAULT_RECONNECT_MULTIPLIER", 2.0),
		DefaultReconnectMaxRetries:   getEnvInt("DEFAULT_RECONNECT_MAX_RETRIES", 0),
		WebPassword:                  os.Getenv("WEB_PASSWORD"),
		SessionSecret:                getEnv("WEB_SESSION_SECRET", os.Getenv("WEB_PASSWORD")),
		CookieSecure:                 getEnvBool("COOKIE_SECURE", true),
		TLSCertPath:                  os.Getenv("TLS_CERT_PATH"),
		TLSKeyPath:                   os.Getenv("TLS_KEY_PATH"),
		ServerMessage:                os.Getenv("SERVER_MESSAGE"),
	}
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes":
			return true
		case "0", "false", "no":
			return false
		}
	}
	return defaultVal
}

func getEnvFloat(key string, defaultVal float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return defaultVal
}

// ValidateAdminConfiguration validates admin config
func (c Config) ValidateAdminConfiguration() error {
	return nil
}
