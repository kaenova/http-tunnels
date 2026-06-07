package protocol

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestCloneHeaders(t *testing.T) {
	headers := http.Header{
		"Content-Type":      {"application/json"},
		"X-Custom":          {"value1", "value2"},
		"Connection":        {"keep-alive"},
		"Transfer-Encoding": {"chunked"},
	}

	cloned := CloneHeaders(headers)

	// Headers needed for websocket upgrade should be preserved; others still stripped.
	if cloned["Connection"][0] != "keep-alive" {
		t.Errorf("Connection should be preserved for upgrade flows: %v", cloned["Connection"])
	}
	if _, ok := cloned["Transfer-Encoding"]; ok {
		t.Error("Transfer-Encoding header should be stripped")
	}

	// Regular headers preserved
	if cloned["Content-Type"][0] != "application/json" {
		t.Errorf("Content-Type not preserved: %v", cloned["Content-Type"])
	}
	if len(cloned["X-Custom"]) != 2 {
		t.Errorf("X-Custom values count: got %d, want 2", len(cloned["X-Custom"]))
	}
}

func TestCloneHeaders_Nil(t *testing.T) {
	cloned := CloneHeaders(nil)
	if cloned != nil {
		t.Error("CloneHeaders(nil) should return nil")
	}
}

func TestApplyHeaders(t *testing.T) {
	dst := http.Header{}
	src := map[string][]string{
		"Content-Type": {"application/json"},
		"X-Custom":     {"val1", "val2"},
		"Connection":   {"keep-alive"},
	}

	ApplyHeaders(dst, src)

	if dst.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type not applied: got %q", dst.Get("Content-Type"))
	}
	if dst.Get("Connection") != "keep-alive" {
		t.Errorf("Connection header should be preserved for upgrade flows: got %q", dst.Get("Connection"))
	}
}

func TestBuildDestinationURL(t *testing.T) {
	tests := []struct {
		name        string
		base        string
		requestPath string
		want        string
	}{
		{
			name:        "simple path",
			base:        "http://localhost:3000",
			requestPath: "/api/users",
			want:        "http://localhost:3000/api/users",
		},
		{
			name:        "path with query",
			base:        "http://localhost:3000",
			requestPath: "/api/users?page=1&limit=10",
			want:        "http://localhost:3000/api/users?page=1&limit=10",
		},
		{
			name:        "base with path prefix",
			base:        "http://localhost:3000/api",
			requestPath: "/users",
			want:        "http://localhost:3000/api/users",
		},
		{
			name:        "root path",
			base:        "http://localhost:3000",
			requestPath: "/",
			want:        "http://localhost:3000/",
		},
		{
			name:        "trailing slash preserved",
			base:        "http://localhost:3000",
			requestPath: "/api/users/",
			want:        "http://localhost:3000/api/users/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, _ := url.Parse(tt.base)
			result, err := BuildDestinationURL(base, tt.requestPath)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.String() != tt.want {
				t.Errorf("got %q, want %q", result.String(), tt.want)
			}
		})
	}
}

func TestNormalizeHost(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"EXAMPLE.COM", "example.com"},
		{"  Example.Com  ", "example.com"},
		{"Test.Host.Com", "test.host.com"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeHost(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeHost(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsTextContentType(t *testing.T) {
	tests := []struct {
		contentType string
		want        bool
	}{
		{"text/plain", true},
		{"text/html; charset=utf-8", true},
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"application/xml", true},
		{"application/javascript", true},
		{"application/x-www-form-urlencoded", true},
		{"application/problem+json", true},
		{"application/vnd.api+json", true},
		{"text/event-stream", true},
		{"image/png", false},
		{"application/octet-stream", false},
		{"video/mp4", false},
		{"audio/mpeg", false},
		{"application/pdf", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			got := IsTextContentType(tt.contentType)
			if got != tt.want {
				t.Errorf("IsTextContentType(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

func TestGenerateID(t *testing.T) {
	id1 := GenerateID(16)
	id2 := GenerateID(16)

	if id1 == "" {
		t.Error("GenerateID returned empty string")
	}
	if id1 == id2 {
		t.Error("GenerateID should produce unique IDs")
	}
	if len(id1) < 16 {
		t.Errorf("GenerateID(16) too short: got %d chars", len(id1))
	}
}

func TestMergeForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/api", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	req.Header.Set("Accept", "application/json")

	headers := MergeForwardedHeaders(nil, req)

	if headers["X-Forwarded-Host"][0] != "example.com" {
		t.Errorf("X-Forwarded-Host: got %q", headers["X-Forwarded-Host"])
	}
	if headers["X-Forwarded-Proto"][0] != "http" {
		t.Errorf("X-Forwarded-Proto: got %q", headers["X-Forwarded-Proto"])
	}
	if len(headers["X-Forwarded-For"]) == 0 {
		t.Error("X-Forwarded-For should be set")
	}
	if headers["Accept"][0] != "application/json" {
		t.Errorf("Accept header not preserved: %v", headers["Accept"])
	}
}
