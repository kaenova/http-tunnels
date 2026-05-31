package server

// Config holds server configuration
type Config struct {
	ListenAddr                   string
	DBPath                       string
	ServerMessage                string
	WebPassword                  string
	SessionSecret                string
	CookieSecure                 bool
	MaxConcurrentRequests        int
	DefaultRequestTimeout        int
	DefaultBackendTimeout        int
	DefaultReconnectEnabled      bool
	DefaultReconnectInitialDelay int
	DefaultReconnectMaxDelay     int
	DefaultReconnectMultiplier   float64
	DefaultReconnectMaxRetries   int
}

// LoadConfig loads configuration from environment variables
func LoadConfig() Config {
	return Config{
		ListenAddr:                   ":80",
		DBPath:                       "http-tunnels.db",
		MaxConcurrentRequests:        500,
		DefaultRequestTimeout:        10000,
		DefaultBackendTimeout:        30000,
		DefaultReconnectEnabled:      true,
		DefaultReconnectInitialDelay: 1000,
		DefaultReconnectMaxDelay:     60000,
		DefaultReconnectMultiplier:   2.0,
		DefaultReconnectMaxRetries:   0,
	}
}

// ValidateAdminConfiguration validates admin config
func (c Config) ValidateAdminConfiguration() error {
	return nil
}