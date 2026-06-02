package integration

import (
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

	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		p50 := percentileDuration(latencies, 0.50)
		p95 := percentileDuration(latencies, 0.95)
		p99 := percentileDuration(latencies, 0.99)
		b.ReportMetric(float64(p50.Microseconds()), "small_p50_us")
		b.ReportMetric(float64(p95.Microseconds()), "small_p95_us")
		b.ReportMetric(float64(p99.Microseconds()), "small_p99_us")
	}
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
