package client

import "testing"

func TestReleaseAssetName(t *testing.T) {
	tests := []struct {
		goos    string
		goarch  string
		want    string
		wantErr bool
	}{
		{"darwin", "arm64", "http-tunnels-darwin-arm64.tar.gz", false},
		{"darwin", "amd64", "http-tunnels-darwin-amd64.tar.gz", false},
		{"linux", "arm64", "http-tunnels-linux-arm64.tar.gz", false},
		{"windows", "amd64", "http-tunnels-windows-amd64.zip", false},
		{"freebsd", "amd64", "", true},
	}

	for _, tt := range tests {
		got, err := releaseAssetName(tt.goos, tt.goarch)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("expected error for %s/%s", tt.goos, tt.goarch)
			}
			continue
		}
		if err != nil {
			t.Fatalf("unexpected error for %s/%s: %v", tt.goos, tt.goarch, err)
		}
		if got != tt.want {
			t.Fatalf("asset mismatch for %s/%s: got %q want %q", tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestFindAssetURL(t *testing.T) {
	release := &githubRelease{
		TagName: "v6.0.0",
		Assets: []githubReleaseAsset{
			{Name: "http-tunnels-darwin-arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin-arm64"},
		},
	}
	url, err := findAssetURL(release, "http-tunnels-darwin-arm64.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://example.com/darwin-arm64" {
		t.Fatalf("unexpected url: %s", url)
	}
}

func TestNormalizeVersion(t *testing.T) {
	if got := normalizeVersion("v6.0.0"); got != "6.0.0" {
		t.Fatalf("unexpected normalized version: %s", got)
	}
	if got := normalizeVersion("6.0.0"); got != "6.0.0" {
		t.Fatalf("unexpected normalized version: %s", got)
	}
}
