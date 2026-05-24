package client

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	selfupdate "github.com/minio/selfupdate"
)

const githubRepo = "kaenova/http-tunnels"

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func RunUpdate() error {
	assetName, err := releaseAssetName()
	if err != nil {
		return err
	}

	release, err := fetchLatestRelease()
	if err != nil {
		return err
	}

	asset, ok := findReleaseAsset(release, assetName)
	if !ok {
		return fmt.Errorf("latest release %s does not include asset %s", release.TagName, assetName)
	}

	fmt.Printf("Downloading %s from %s\n", asset.Name, release.TagName)
	archiveBytes, err := downloadAsset(asset.BrowserDownloadURL)
	if err != nil {
		return err
	}

	binaryBytes, err := extractBinaryFromArchive(asset.Name, archiveBytes)
	if err != nil {
		return err
	}

	if err := selfupdate.Apply(bytes.NewReader(binaryBytes), selfupdate.Options{}); err != nil {
		if rollbackErr := selfupdate.RollbackError(err); rollbackErr != nil {
			return fmt.Errorf("update failed: %w (rollback failed: %v)", err, rollbackErr)
		}
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("Updated http-tunnels to %s\n", release.TagName)
	return nil
}

func releaseAssetName() (string, error) {
	osName := runtime.GOOS
	archName := runtime.GOARCH

	switch osName {
	case "darwin", "linux":
		return fmt.Sprintf("http-tunnels-%s-%s.tar.gz", osName, archName), nil
	case "windows":
		return fmt.Sprintf("http-tunnels-%s-%s.zip", osName, archName), nil
	default:
		return "", fmt.Errorf("unsupported operating system: %s", osName)
	}
}

func fetchLatestRelease() (*githubRelease, error) {
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "http-tunnels-update")

	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetching latest release failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4*1024))
		return nil, fmt.Errorf("fetching latest release failed: %s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	var release githubRelease
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decoding latest release failed: %w", err)
	}
	return &release, nil
}

func findReleaseAsset(release *githubRelease, assetName string) (*githubAsset, bool) {
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			return &asset, true
		}
	}
	return nil, false
}

func downloadAsset(downloadURL string) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "http-tunnels-update")

	client := &http.Client{Timeout: 2 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("downloading release asset failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4*1024))
		return nil, fmt.Errorf("downloading release asset failed: %s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading release asset failed: %w", err)
	}
	return data, nil
}

func extractBinaryFromArchive(assetName string, archive []byte) ([]byte, error) {
	switch {
	case strings.HasSuffix(assetName, ".tar.gz"):
		return extractTarGzBinary(archive)
	case strings.HasSuffix(assetName, ".zip"):
		return extractZipBinary(archive)
	default:
		return archive, nil
	}
}

func extractTarGzBinary(archive []byte) ([]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("opening tar.gz archive failed: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("reading tar.gz archive failed: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, fmt.Errorf("extracting binary from tar.gz failed: %w", err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("no binary found in tar.gz archive")
}

func extractZipBinary(archive []byte) ([]byte, error) {
	zipReader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("opening zip archive failed: %w", err)
	}

	for _, file := range zipReader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		name := strings.ToLower(filepath.Base(file.Name))
		if !strings.HasSuffix(name, ".exe") && !strings.HasPrefix(name, "http-tunnels") {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("opening binary in zip failed: %w", err)
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("extracting binary from zip failed: %w", err)
		}
		return data, nil
	}

	return nil, fmt.Errorf("no binary found in zip archive")
}
