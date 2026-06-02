package integration

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"
)

func BenchmarkLargeDownloadOnly(b *testing.B) {
	const dataSize = 64 * 1024 * 1024
	largeData := make([]byte, dataSize)

	h := NewHarnessWithBackendTB(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", itoa(len(largeData)))
			_, _ = w.Write(largeData)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	tunnel := h.CreateTunnelTB(b, "bench-large")
	h.ConnectAndRegisterTB(b, tunnel)

	b.SetBytes(int64(dataSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := h.HTTPGetTB(b, tunnel.Domain, "/big")
		if err != nil {
			b.Fatalf("download request failed: %v", err)
		}
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err != nil {
			b.Fatalf("download read failed: %v", err)
		}
	}
}

func BenchmarkLargeDownloadWithSpikeTraffic(b *testing.B) {
	const (
		dataSize          = 64 * 1024 * 1024
		spikePerIteration = 24
	)
	largeData := make([]byte, dataSize)

	h := NewHarnessWithBackendTB(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", itoa(len(largeData)))
			_, _ = w.Write(largeData)
		case "/small":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	tunnel := h.CreateTunnelTB(b, "bench-mixed")
	h.ConnectAndRegisterTB(b, tunnel)

	latencies := make([]time.Duration, 0, b.N*spikePerIteration)
	var latMu sync.Mutex

	b.SetBytes(int64(dataSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := h.HTTPGetTB(b, tunnel.Domain, "/big")
			if err != nil {
				b.Errorf("big download request failed: %v", err)
				return
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				b.Errorf("big download read failed: %v", err)
			}
		}()

		time.Sleep(25 * time.Millisecond)

		for j := 0; j < spikePerIteration; j++ {
			start := time.Now()
			resp, err := h.HTTPGetTB(b, tunnel.Domain, "/small")
			if err != nil {
				b.Fatalf("small spike request failed: %v", err)
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				b.Fatalf("small spike read failed: %v", err)
			}
			latMu.Lock()
			latencies = append(latencies, time.Since(start))
			latMu.Unlock()
		}

		wg.Wait()
	}
	b.StopTimer()
	reportLatencyMetrics(b, "small", latencies)
}

func BenchmarkParallelLargeDownloadsWithSpikeTraffic(b *testing.B) {
	const (
		dataSize          = 32 * 1024 * 1024
		parallelLarge     = 4
		spikePerIteration = 32
	)
	largeData := make([]byte, dataSize)

	h := NewHarnessWithBackendTB(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", itoa(len(largeData)))
			_, _ = w.Write(largeData)
		case "/small":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	tunnel := h.CreateTunnelTB(b, "bench-parallel-large")
	h.ConnectAndRegisterTB(b, tunnel)

	latencies := make([]time.Duration, 0, b.N*spikePerIteration)
	var latMu sync.Mutex

	b.SetBytes(int64(parallelLarge * dataSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		wg.Add(parallelLarge)
		for j := 0; j < parallelLarge; j++ {
			go func() {
				defer wg.Done()
				resp, err := h.HTTPGetTB(b, tunnel.Domain, "/big")
				if err != nil {
					b.Errorf("parallel big download request failed: %v", err)
					return
				}
				_, err = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if err != nil {
					b.Errorf("parallel big download read failed: %v", err)
				}
			}()
		}

		time.Sleep(25 * time.Millisecond)

		for j := 0; j < spikePerIteration; j++ {
			start := time.Now()
			resp, err := h.HTTPGetTB(b, tunnel.Domain, "/small")
			if err != nil {
				b.Fatalf("small spike request failed: %v", err)
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				b.Fatalf("small spike read failed: %v", err)
			}
			latMu.Lock()
			latencies = append(latencies, time.Since(start))
			latMu.Unlock()
		}

		wg.Wait()
	}
	b.StopTimer()
	reportLatencyMetrics(b, "small", latencies)
}

func BenchmarkLargeUploadDownloadWithSpikeTraffic(b *testing.B) {
	const (
		uploadSize        = 32 * 1024 * 1024
		downloadSize      = 32 * 1024 * 1024
		spikePerIteration = 32
	)
	uploadPayload := bytes.Repeat([]byte("u"), uploadSize)
	downloadPayload := make([]byte, downloadSize)

	h := NewHarnessWithBackendTB(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload":
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uploaded":true}`))
		case "/download":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", itoa(len(downloadPayload)))
			_, _ = w.Write(downloadPayload)
		case "/small":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	tunnel := h.CreateTunnelTB(b, "bench-upload-download")
	h.ConnectAndRegisterTB(b, tunnel)

	latencies := make([]time.Duration, 0, b.N*spikePerIteration)
	var latMu sync.Mutex

	b.SetBytes(int64(uploadSize + downloadSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			resp, err := h.HTTPPostTB(b, tunnel.Domain, "/upload", "application/octet-stream", bytes.NewReader(uploadPayload))
			if err != nil {
				b.Errorf("large upload failed: %v", err)
				return
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				b.Errorf("large upload response read failed: %v", err)
			}
		}()

		go func() {
			defer wg.Done()
			resp, err := h.HTTPGetTB(b, tunnel.Domain, "/download")
			if err != nil {
				b.Errorf("large download failed: %v", err)
				return
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				b.Errorf("large download read failed: %v", err)
			}
		}()

		time.Sleep(25 * time.Millisecond)

		for j := 0; j < spikePerIteration; j++ {
			start := time.Now()
			resp, err := h.HTTPGetTB(b, tunnel.Domain, "/small")
			if err != nil {
				b.Fatalf("small spike request failed: %v", err)
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				b.Fatalf("small spike read failed: %v", err)
			}
			latMu.Lock()
			latencies = append(latencies, time.Since(start))
			latMu.Unlock()
		}

		wg.Wait()
	}
	b.StopTimer()
	reportLatencyMetrics(b, "small", latencies)
}

func BenchmarkSSEWithLargeDownloadAndSpikeTraffic(b *testing.B) {
	const (
		downloadSize      = 32 * 1024 * 1024
		spikePerIteration = 24
		sseEvents         = 64
	)
	downloadPayload := make([]byte, downloadSize)

	h := NewHarnessWithBackendTB(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			flusher, _ := w.(http.Flusher)
			for i := 0; i < sseEvents; i++ {
				_, _ = fmt.Fprintf(w, "data: event-%d\n\n", i)
				if flusher != nil {
					flusher.Flush()
				}
				time.Sleep(2 * time.Millisecond)
			}
		case "/download":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", itoa(len(downloadPayload)))
			_, _ = w.Write(downloadPayload)
		case "/small":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	tunnel := h.CreateTunnelTB(b, "bench-sse-mixed")
	h.ConnectAndRegisterTB(b, tunnel)

	latencies := make([]time.Duration, 0, b.N*spikePerIteration)
	var latMu sync.Mutex

	b.SetBytes(int64(downloadSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			resp, err := h.HTTPGetTB(b, tunnel.Domain, "/sse")
			if err != nil {
				b.Errorf("sse request failed: %v", err)
				return
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				b.Errorf("sse read failed: %v", err)
			}
		}()

		go func() {
			defer wg.Done()
			resp, err := h.HTTPGetTB(b, tunnel.Domain, "/download")
			if err != nil {
				b.Errorf("download request failed: %v", err)
				return
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				b.Errorf("download read failed: %v", err)
			}
		}()

		time.Sleep(25 * time.Millisecond)

		for j := 0; j < spikePerIteration; j++ {
			start := time.Now()
			resp, err := h.HTTPGetTB(b, tunnel.Domain, "/small")
			if err != nil {
				b.Fatalf("small spike request failed: %v", err)
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				b.Fatalf("small spike read failed: %v", err)
			}
			latMu.Lock()
			latencies = append(latencies, time.Since(start))
			latMu.Unlock()
		}

		wg.Wait()
	}
	b.StopTimer()
	reportLatencyMetrics(b, "small", latencies)
}

func reportLatencyMetrics(b *testing.B, prefix string, latencies []time.Duration) {
	if len(latencies) == 0 {
		return
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := percentileDuration(latencies, 0.50)
	p95 := percentileDuration(latencies, 0.95)
	p99 := percentileDuration(latencies, 0.99)
	b.ReportMetric(float64(p50.Microseconds()), prefix+"_p50_us")
	b.ReportMetric(float64(p95.Microseconds()), prefix+"_p95_us")
	b.ReportMetric(float64(p99.Microseconds()), prefix+"_p99_us")
}

func percentileDuration(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		return values[0]
	}
	if p >= 1 {
		return values[len(values)-1]
	}
	idx := int(float64(len(values)-1) * p)
	return values[idx]
}
