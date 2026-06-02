package client

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	githubOwner = "kaenova"
	githubRepo  = "http-tunnels"
)

type UpdateOptions struct {
	TargetVersion string
	Force         bool
}

type githubRelease struct {
	TagName string               `json:"tag_name"`
	Assets  []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func RunUpdate(ctx context.Context, opts UpdateOptions) error {
	httpClient := &http.Client{Timeout: 30 * time.Second}

	fmt.Fprintf(os.Stderr, "Current version: %s\n", Version)
	fmt.Fprintln(os.Stderr, "Checking release metadata...")

	release, err := fetchRelease(ctx, httpClient, strings.TrimSpace(opts.TargetVersion))
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Target version: %s\n", release.TagName)

	if !opts.Force && Version != "dev" && normalizeVersion(Version) == normalizeVersion(release.TagName) {
		fmt.Fprintf(os.Stderr, "Already up to date: %s\n", release.TagName)
		return nil
	}

	assetName, err := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	assetURL, err := findAssetURL(release, assetName)
	if err != nil {
		return err
	}

	currentPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating current executable failed: %w", err)
	}
	if resolvedPath, resolveErr := filepath.EvalSymlinks(currentPath); resolveErr == nil && strings.TrimSpace(resolvedPath) != "" {
		currentPath = resolvedPath
	}

	tmpDir, err := os.MkdirTemp("", "http-tunnels-update-")
	if err != nil {
		return fmt.Errorf("creating temp dir failed: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, assetName)
	fmt.Fprintf(os.Stderr, "Downloading: %s\n", assetName)
	if err := downloadFile(ctx, httpClient, assetURL, archivePath); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "Extracting update archive...")
	extractedPath, err := extractArchive(archivePath, tmpDir, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		fallbackPath := currentPath + ".new.exe"
		if err := copyFile(extractedPath, fallbackPath, 0o755); err != nil {
			return fmt.Errorf("writing downloaded binary for windows failed: %w", err)
		}
		return fmt.Errorf("self-update replacement is not supported in-place on windows yet; new binary saved to %s", fallbackPath)
	}

	fmt.Fprintf(os.Stderr, "Replacing binary at: %s\n", currentPath)
	if err := replaceExecutable(currentPath, extractedPath); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Update complete: %s -> %s\n", Version, release.TagName)
	return nil
}

func fetchRelease(ctx context.Context, httpClient *http.Client, targetVersion string) (*githubRelease, error) {
	var releaseURL string
	if targetVersion == "" {
		releaseURL = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", githubOwner, githubRepo)
	} else {
		releaseURL = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", githubOwner, githubRepo, targetVersion)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating release metadata request failed: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "http-tunnels-updater")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching release metadata failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetching release metadata failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decoding release metadata failed: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return nil, errors.New("release metadata missing tag name")
	}
	return &release, nil
}

func releaseAssetName(goos, goarch string) (string, error) {
	switch goos {
	case "darwin", "linux":
		switch goarch {
		case "amd64", "arm64":
			return fmt.Sprintf("http-tunnels-%s-%s.tar.gz", goos, goarch), nil
		}
	case "windows":
		switch goarch {
		case "amd64", "arm64":
			return fmt.Sprintf("http-tunnels-%s-%s.zip", goos, goarch), nil
		}
	}
	return "", fmt.Errorf("self-update is not supported for %s/%s", goos, goarch)
}

func findAssetURL(release *githubRelease, assetName string) (string, error) {
	for _, asset := range release.Assets {
		if asset.Name == assetName && strings.TrimSpace(asset.BrowserDownloadURL) != "" {
			return asset.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("release %s does not contain asset %s", release.TagName, assetName)
}

func downloadFile(ctx context.Context, httpClient *http.Client, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating download request failed: %w", err)
	}
	req.Header.Set("User-Agent", "http-tunnels-updater")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading release asset failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("downloading release asset failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("creating downloaded file failed: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("writing downloaded file failed: %w", err)
	}
	return nil
}

func extractArchive(archivePath, destDir, goos, goarch string) (string, error) {
	assetBase := fmt.Sprintf("http-tunnels-%s-%s", goos, goarch)
	if goos == "windows" {
		assetBase += ".exe"
	}
	if strings.HasSuffix(archivePath, ".zip") {
		return extractZip(archivePath, destDir, assetBase)
	}
	return extractTarGz(archivePath, destDir, assetBase)
}

func extractTarGz(archivePath, destDir, expectedName string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("opening archive failed: %w", err)
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("opening gzip archive failed: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading tar archive failed: %w", err)
		}
		if header == nil || header.FileInfo().IsDir() {
			continue
		}
		if filepath.Base(header.Name) != expectedName {
			continue
		}
		destPath := filepath.Join(destDir, expectedName)
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", fmt.Errorf("creating extracted binary failed: %w", err)
		}
		if _, err := io.Copy(out, tarReader); err != nil {
			out.Close()
			return "", fmt.Errorf("extracting binary failed: %w", err)
		}
		if err := out.Close(); err != nil {
			return "", fmt.Errorf("closing extracted binary failed: %w", err)
		}
		return destPath, nil
	}
	return "", fmt.Errorf("binary %s not found in archive", expectedName)
}

func extractZip(archivePath, destDir, expectedName string) (string, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("opening zip archive failed: %w", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		if file.FileInfo().IsDir() || filepath.Base(file.Name) != expectedName {
			continue
		}
		src, err := file.Open()
		if err != nil {
			return "", fmt.Errorf("opening zip entry failed: %w", err)
		}
		defer src.Close()
		destPath := filepath.Join(destDir, expectedName)
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", fmt.Errorf("creating extracted binary failed: %w", err)
		}
		if _, err := io.Copy(out, src); err != nil {
			out.Close()
			return "", fmt.Errorf("extracting binary failed: %w", err)
		}
		if err := out.Close(); err != nil {
			return "", fmt.Errorf("closing extracted binary failed: %w", err)
		}
		return destPath, nil
	}
	return "", fmt.Errorf("binary %s not found in archive", expectedName)
}

func replaceExecutable(currentPath, newBinaryPath string) error {
	newPath := currentPath + ".new"
	backupPath := currentPath + ".old"

	if err := copyFile(newBinaryPath, newPath, 0o755); err != nil {
		return fmt.Errorf("preparing replacement binary failed: %w", err)
	}
	_ = os.Remove(backupPath)
	if err := os.Rename(currentPath, backupPath); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("backing up current binary failed: %w", err)
	}
	if err := os.Rename(newPath, currentPath); err != nil {
		_ = os.Rename(backupPath, currentPath)
		_ = os.Remove(newPath)
		return fmt.Errorf("installing updated binary failed: %w", err)
	}
	_ = os.Remove(backupPath)
	return nil
}

func copyFile(srcPath, dstPath string, mode os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, src); err != nil {
		return err
	}
	return out.Close()
}

func normalizeVersion(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "v")
}
