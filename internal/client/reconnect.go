package client

import (
	"math"
	"math/rand"
	"time"
)

// ReconnectConfig holds reconnect settings
type ReconnectConfig struct {
	Enabled        bool
	InitialDelayMs int
	MaxDelayMs     int
	Multiplier     float64
	MaxRetries     int
	Jitter         bool
}

// DefaultReconnectConfig returns default reconnect settings
func DefaultReconnectConfig() ReconnectConfig {
	return ReconnectConfig{
		Enabled:        true,
		InitialDelayMs: 1000,
		MaxDelayMs:     60000,
		Multiplier:     2.0,
		MaxRetries:     0, // unlimited
		Jitter:         true,
	}
}

// CalculateDelay computes the reconnect delay for a given attempt
func (c ReconnectConfig) CalculateDelay(attempt int) time.Duration {
	if !c.Enabled {
		return 0
	}
	if c.MaxRetries > 0 && attempt >= c.MaxRetries {
		return 0
	}

	base := float64(c.InitialDelayMs) * math.Pow(c.Multiplier, float64(attempt))
	if base > float64(c.MaxDelayMs) {
		base = float64(c.MaxDelayMs)
	}

	if c.Jitter {
		jitter := 0.5 + rand.Float64()*0.5
		base = base * jitter
	}

	return time.Duration(base) * time.Millisecond
}

// ShouldRetry returns true if another reconnect attempt should be made
func (c ReconnectConfig) ShouldRetry(attempt int) bool {
	if !c.Enabled {
		return false
	}
	if c.MaxRetries > 0 && attempt >= c.MaxRetries {
		return false
	}
	return true
}