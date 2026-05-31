package client

import (
	"testing"
	"time"
)

func TestReconnectBackoff_NoJitter(t *testing.T) {
	cfg := ReconnectConfig{
		Enabled:        true,
		InitialDelayMs: 1000,
		MaxDelayMs:     60000,
		Multiplier:     2.0,
		MaxRetries:     0,
		Jitter:         false,
	}

	tests := []struct {
		attempt int
		wantMs  int
	}{
		{0, 1000},
		{1, 2000},
		{2, 4000},
		{3, 8000},
		{4, 16000},
		{5, 32000},
		{6, 60000},
		{7, 60000},
		{10, 60000},
	}

	for _, tt := range tests {
		got := cfg.CalculateDelay(tt.attempt)
		if got != time.Duration(tt.wantMs)*time.Millisecond {
			t.Errorf("attempt %d: got %v, want %v", tt.attempt, got, time.Duration(tt.wantMs)*time.Millisecond)
		}
	}
}

func TestReconnectBackoff_WithJitter(t *testing.T) {
	cfg := ReconnectConfig{
		Enabled:        true,
		InitialDelayMs: 1000,
		MaxDelayMs:     60000,
		Multiplier:     2.0,
		MaxRetries:     0,
		Jitter:         true,
	}

	for i := 0; i < 100; i++ {
		delay := cfg.CalculateDelay(2)
		baseMs := 4000 * time.Millisecond
		minMs := baseMs / 2
		maxMs := baseMs

		if delay < minMs {
			t.Errorf("attempt 2 jitter too low: got %v, min %v", delay, minMs)
		}
		if delay > maxMs {
			t.Errorf("attempt 2 jitter too high: got %v, max %v", delay, maxMs)
		}
	}
}

func TestReconnectBackoff_Disabled(t *testing.T) {
	cfg := ReconnectConfig{Enabled: false}
	delay := cfg.CalculateDelay(5)
	if delay != 0 {
		t.Errorf("disabled reconnect should return 0 delay, got %v", delay)
	}
}

func TestReconnectBackoff_MaxRetries(t *testing.T) {
	cfg := ReconnectConfig{
		Enabled:        true,
		InitialDelayMs: 100,
		MaxDelayMs:     1000,
		Multiplier:     2.0,
		MaxRetries:     3,
		Jitter:         false,
	}

	if cfg.CalculateDelay(0) == 0 {
		t.Error("attempt 0 should be valid")
	}
	if cfg.CalculateDelay(2) == 0 {
		t.Error("attempt 2 should be valid")
	}
	if cfg.CalculateDelay(3) != 0 {
		t.Error("attempt 3 should be 0 (max retries exceeded)")
	}
}

func TestReconnectBackoff_MaxRetriesUnlimited(t *testing.T) {
	cfg := ReconnectConfig{
		Enabled:        true,
		InitialDelayMs: 100,
		MaxDelayMs:     1000,
		Multiplier:     2.0,
		MaxRetries:     0,
		Jitter:         false,
	}

	for i := 0; i < 100; i++ {
		if cfg.CalculateDelay(i) == 0 {
			t.Errorf("attempt %d should be valid with unlimited retries", i)
			break
		}
	}
}

func TestShouldRetry(t *testing.T) {
	// Disabled
	cfg := ReconnectConfig{Enabled: false}
	if cfg.ShouldRetry(0) {
		t.Error("disabled should not retry")
	}

	// Limited retries
	cfg = ReconnectConfig{
		Enabled:    true,
		MaxRetries: 3,
	}
	if !cfg.ShouldRetry(0) {
		t.Error("attempt 0 should retry")
	}
	if !cfg.ShouldRetry(2) {
		t.Error("attempt 2 should retry")
	}
	if cfg.ShouldRetry(3) {
		t.Error("attempt 3 should not retry")
	}

	// Unlimited
	cfg.MaxRetries = 0
	if !cfg.ShouldRetry(100) {
		t.Error("unlimited should always retry")
	}
}