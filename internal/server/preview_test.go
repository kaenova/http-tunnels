package server

import "testing"

func TestShouldFlushStreamingResponse(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		want        bool
	}{
		{name: "sse", contentType: "text/event-stream", want: true},
		{name: "sse with spaces", contentType: " text/event-stream ", want: true},
		{name: "json", contentType: "application/json", want: false},
		{name: "binary", contentType: "application/octet-stream", want: false},
		{name: "empty", contentType: "", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldFlushStreamingResponse(tc.contentType); got != tc.want {
				t.Fatalf("shouldFlushStreamingResponse(%q) = %v, want %v", tc.contentType, got, tc.want)
			}
		})
	}
}
