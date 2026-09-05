package storage

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/subhammahanty235/lilio/pkg/metadata"
)

// Benchmark: Single File Upload (Various Sizes)
func BenchmarkPutObject_1KB(b *testing.B) {
	benchmarkPutObject(b, 1*1024)
}

func BenchmarkPutObject_100KB(b *testing.B) {
	benchmarkPutObject(b, 100*1024)
}

func BenchmarkPutObject_1MB(b *testing.B) {
	benchmarkPutObject(b, 1*1024*1024)
}

func BenchmarkPutObject_10MB(b *testing.B) {
	benchmarkPutObject(b, 10*1024*1024)
}

func BenchmarkPutObject_100MB(b *testing.B) {
	benchmarkPutObject(b, 100*1024*1024)
}

func benchmarkPutObject(b *testing.B, size int64) {
	lilio := setupBenchLilio(b, 3, 2, 2)
	defer cleanupBench(lilio)

	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	b.SetBytes(size)

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("bench-key-%d", i)
		reader := bytes.NewReader(data)
		_, err := lilio.PutObject("bench-bucket", key, reader, size, "application/octet-stream")
		if err != nil {
			b.Fatalf("PutObject failed: %v", err)
		}
	}
}

// Benchmark: Single File Download (Various Sizes)
func BenchmarkGetObject_1KB(b *testing.B) {
	benchmarkGetObject(b, 1*1024)
}

func BenchmarkGetObject_100KB(b *testing.B) {
	benchmarkGetObject(b, 100*1024)
}

func BenchmarkGetObject_1MB(b *testing.B) {
	benchmarkGetObject(b, 1*1024*1024)
}

func BenchmarkGetObject_10MB(b *testing.B) {
	benchmarkGetObject(b, 10*1024*1024)
}

func BenchmarkGetObject_100MB(b *testing.B) {
	benchmarkGetObject(b, 100*1024*1024)
}

func benchmarkGetObject(b *testing.B, size int64) {
	lilio := setupBenchLilio(b, 3, 2, 2)
	defer cleanupBench(lilio)

	// Upload test data once
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}

	_, err := lilio.PutObject("bench-bucket", "get-bench-key", bytes.NewReader(data), size, "application/octet-stream")
	if err != nil {
		b.Fatalf("Setup failed: %v", err)
	}

	b.ResetTimer()
	b.SetBytes(size)

	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		err := lilio.GetObject("bench-bucket", "get-bench-key", &buf)
		if err != nil {
			b.Fatalf("GetObject failed: %v", err)
		}
	}
}

// Benchmark: Concurrent Uploads (Stress Test)
func BenchmarkConcurrentUploads_10_1MB(b *testing.B) {
	benchmarkConcurrentUploads(b, 10, 1*1024*1024)
}

func BenchmarkConcurrentUploads_50_1MB(b *testing.B) {
	benchmarkConcurrentUploads(b, 50, 1*1024*1024)
}

func BenchmarkConcurrentUploads_100_1MB(b *testing.B) {
	benchmarkConcurrentUploads(b, 100, 1*1024*1024)
}

func benchmarkConcurrentUploads(b *testing.B, concurrency int, size int64) {
	lilio := setupBenchLilio(b, 3, 2, 2)
	defer cleanupBench(lilio)

	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	b.SetBytes(size * int64(concurrency))

	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		errChan := make(chan error, concurrency)

		for j := 0; j < concurrency; j++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				key := fmt.Sprintf("concurrent-key-%d-%d", i, id)
				reader := bytes.NewReader(data)
				_, err := lilio.PutObject("bench-bucket", key, reader, size, "application/octet-stream")
				if err != nil {
					errChan <- err
				}
			}(j)
		}

		wg.Wait()
		close(errChan)

		for err := range errChan {
			b.Fatalf("Concurrent upload failed: %v", err)
		}
	}
}

// Benchmark: Quorum Performance Overhead
func BenchmarkQuorumOverhead_NoQuorum(b *testing.B) {
	// N=3, W=1, R=1 (minimal quorum)
	benchmarkQuorumOverhead(b, 3, 1, 1)
}

func BenchmarkQuorumOverhead_Majority(b *testing.B) {
	// N=3, W=2, R=2 (majority quorum)
	benchmarkQuorumOverhead(b, 3, 2, 2)
}

func BenchmarkQuorumOverhead_All(b *testing.B) {
	// N=3, W=3, R=3 (all nodes)
	benchmarkQuorumOverhead(b, 3, 3, 3)
}

func benchmarkQuorumOverhead(b *testing.B, n, w, r int) {
	lilio := setupBenchLilio(b, n, w, r)
	defer cleanupBench(lilio)

	size := int64(1 * 1024 * 1024) // 1MB
	data := make([]byte, size)

	b.ResetTimer()
	b.SetBytes(size)

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("quorum-bench-%d", i)
		reader := bytes.NewReader(data)
		_, err := lilio.PutObject("bench-bucket", key, reader, size, "application/octet-stream")
		if err != nil {
			b.Fatalf("PutObject failed: %v", err)
		}
	}
}

// Benchmark: Metadata Operations
func BenchmarkMetadataSave(b *testing.B) {
	lilio := setupBenchLilio(b, 3, 2, 2)
	defer cleanupBench(lilio)

	meta := &metadata.ObjectMetadata{
		ObjectID:    "bench-object-id",
		Bucket:      "bench-bucket",
		Key:         "bench-key",
		Size:        1024 * 1024,
		Checksum:    "abc123",
		ChunkSize:   1024 * 1024,
		TotalChunks: 1,
		Chunks: []metadata.ChunkInfo{
			{
				ChunkID:      "chunk-1",
				ChunkIndex:   0,
				Size:         1024 * 1024,
				Checksum:     "chunk-checksum",
				StorageNodes: []string{"node-1", "node-2", "node-3"},
				Version:      time.Now().UnixNano(),
			},
		},
		CreatedAt:   time.Now(),
		ContentType: "application/octet-stream",
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		meta.Key = fmt.Sprintf("bench-key-%d", i)
		err := lilio.Metadata.SaveObjectMetadata(meta)
		if err != nil {
			b.Fatalf("SaveObjectMetadata failed: %v", err)
		}
	}
}

func BenchmarkMetadataGet(b *testing.B) {
	lilio := setupBenchLilio(b, 3, 2, 2)
	defer cleanupBench(lilio)

	// Save metadata once
	meta := &metadata.ObjectMetadata{
		ObjectID:    "bench-object-id",
		Bucket:      "bench-bucket",
		Key:         "bench-get-key",
		Size:        1024 * 1024,
		Checksum:    "abc123",
		ChunkSize:   1024 * 1024,
		TotalChunks: 1,
		Chunks: []metadata.ChunkInfo{
			{
				ChunkID:      "chunk-1",
				ChunkIndex:   0,
				Size:         1024 * 1024,
				Checksum:     "chunk-checksum",
				StorageNodes: []string{"node-1", "node-2", "node-3"},
				Version:      time.Now().UnixNano(),
			},
		},
		CreatedAt:   time.Now(),
		ContentType: "application/octet-stream",
	}
	lilio.Metadata.SaveObjectMetadata(meta)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := lilio.Metadata.GetObjectMetadata("bench-bucket", "bench-get-key")
		if err != nil {
			b.Fatalf("GetObjectMetadata failed: %v", err)
		}
	}
}

// Benchmark: Memory Allocation
func BenchmarkMemoryAllocation_1MB(b *testing.B) {
	benchmarkMemoryAllocation(b, 1*1024*1024)
}

func BenchmarkMemoryAllocation_10MB(b *testing.B) {
	benchmarkMemoryAllocation(b, 10*1024*1024)
}

func BenchmarkMemoryAllocation_100MB(b *testing.B) {
	benchmarkMemoryAllocation(b, 100*1024*1024)
}

func benchmarkMemoryAllocation(b *testing.B, size int64) {
	lilio := setupBenchLilio(b, 3, 2, 2)
	defer cleanupBench(lilio)

	data := make([]byte, size)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("mem-bench-%d", i)
		reader := bytes.NewReader(data)
		_, err := lilio.PutObject("bench-bucket", key, reader, size, "application/octet-stream")
		if err != nil {
			b.Fatalf("PutObject failed: %v", err)
		}
	}
}

// Stress Test: Maximum Concurrent Operations
func BenchmarkStressTest_MaxConcurrency(b *testing.B) {
	lilio := setupBenchLilio(b, 3, 2, 2)
	defer cleanupBench(lilio)

	concurrency := 200 // High concurrency
	size := int64(512 * 1024) // 512KB per file

	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	b.SetBytes(size * int64(concurrency))

	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		errChan := make(chan error, concurrency)
		start := time.Now()

		for j := 0; j < concurrency; j++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				key := fmt.Sprintf("stress-key-%d-%d", i, id)
				reader := bytes.NewReader(data)
				_, err := lilio.PutObject("bench-bucket", key, reader, size, "application/octet-stream")
				if err != nil {
					errChan <- err
				}
			}(j)
		}

		wg.Wait()
		close(errChan)
		duration := time.Since(start)

		failCount := 0
		for err := range errChan {
			failCount++
			if failCount == 1 {
				b.Logf("First error: %v", err)
			}
		}

		if failCount > 0 {
			b.Logf("Iteration %d: %d/%d operations failed in %v", i, failCount, concurrency, duration)
		} else {
			b.Logf("Iteration %d: All %d operations succeeded in %v (%.2f ops/sec)",
				i, concurrency, duration, float64(concurrency)/duration.Seconds())
		}
	}
}

// Benchmark: Throughput Test (Total MB/s)
func BenchmarkThroughput_Sequential(b *testing.B) {
	lilio := setupBenchLilio(b, 3, 2, 2)
	defer cleanupBench(lilio)

	size := int64(10 * 1024 * 1024) // 10MB
	data := make([]byte, size)

	b.ResetTimer()
	b.SetBytes(size)

	start := time.Now()
	totalBytes := int64(0)

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("throughput-key-%d", i)
		reader := bytes.NewReader(data)
		_, err := lilio.PutObject("bench-bucket", key, reader, size, "application/octet-stream")
		if err != nil {
			b.Fatalf("PutObject failed: %v", err)
		}
		totalBytes += size
	}

	duration := time.Since(start)
	throughput := float64(totalBytes) / duration.Seconds() / (1024 * 1024) // MB/s

	b.ReportMetric(throughput, "MB/s")
}

// Helper functions
func setupBenchLilio(b *testing.B, n, w, r int) *Lilio {
	tempDir := b.TempDir()

	cfg := Config{
		BasePath:          tempDir,
		ChunkSize:         1024 * 1024, // 1MB chunks
		ReplicationFactor: n,
		Quorum:            &QuorumConfig{N: n, W: w, R: r},
		MetadataConfig: &metadata.Config{
			Type: metadata.StoreTypeMemory,
		},
	}

	lilio, err := NewLilioInstance(cfg)
	if err != nil {
		b.Fatalf("Failed to create Lilio instance: %v", err)
	}

	// Add mock backends
	for i := 0; i < n; i++ {
		backend := &MockBackend{
			name:   fmt.Sprintf("bench-backend-%d", i),
			chunks: make(map[string][]byte),
		}
		lilio.AddBackend(backend)
	}

	// Create test bucket
	if err := lilio.CreateBucket("bench-bucket"); err != nil {
		b.Fatalf("Failed to create bucket: %v", err)
	}

	return lilio
}

func cleanupBench(lilio *Lilio) {
	if lilio.Metadata != nil {
		lilio.Metadata.Close()
	}
	os.RemoveAll(lilio.BasePath)
}
