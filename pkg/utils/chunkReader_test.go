package utils

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"
)

// TestChunkReaderBasic tests basic chunking functionality
func TestChunkReaderBasic(t *testing.T) {
	// Test data: 2.5 MB (will create 3 chunks of 1MB each: 1MB, 1MB, 0.5MB)
	testSize := 2*1024*1024 + 512*1024 // 2.5 MB
	testData := make([]byte, testSize)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	reader := bytes.NewReader(testData)
	chunkReader := NewChunkReader(reader, 1024*1024) // 1MB chunks

	var reconstructed bytes.Buffer
	chunkCount := 0

	for {
		chunk, index, err := chunkReader.NextChunk()
		if err == io.EOF && chunk == nil {
			break
		}

		if err != nil && err != io.EOF {
			t.Fatalf("Unexpected error: %v", err)
		}

		if index != chunkCount {
			t.Errorf("Expected chunk index %d, got %d", chunkCount, index)
		}

		reconstructed.Write(chunk)
		chunkCount++

		t.Logf("Chunk %d: %d bytes", index, len(chunk))
	}

	// Verify we got 3 chunks
	if chunkCount != 3 {
		t.Errorf("Expected 3 chunks, got %d", chunkCount)
	}

	// Verify chunk sizes
	// Chunks: 1MB, 1MB, 0.5MB
	expectedSizes := []int{1024 * 1024, 1024 * 1024, 512 * 1024}
	reader2 := bytes.NewReader(testData)
	chunkReader2 := NewChunkReader(reader2, 1024*1024)

	for i := 0; i < 3; i++ {
		chunk, _, _ := chunkReader2.NextChunk()
		if len(chunk) != expectedSizes[i] {
			t.Errorf("Chunk %d: expected %d bytes, got %d", i, expectedSizes[i], len(chunk))
		}
	}

	// Verify data integrity
	if !bytes.Equal(testData, reconstructed.Bytes()) {
		t.Error("Reconstructed data doesn't match original")
	}

	t.Logf("✓ ChunkReader correctly split %d bytes into %d chunks", testSize, chunkCount)
}

// TestChunkReaderExactSize tests when file size is exact multiple of chunk size
func TestChunkReaderExactSize(t *testing.T) {
	testSize := 3 * 1024 * 1024 // Exactly 3MB
	testData := make([]byte, testSize)
	rand.Read(testData)

	reader := bytes.NewReader(testData)
	chunkReader := NewChunkReader(reader, 1024*1024) // 1MB chunks

	chunkCount := 0
	var reconstructed bytes.Buffer

	for {
		chunk, index, err := chunkReader.NextChunk()
		if err == io.EOF && chunk == nil {
			break
		}

		if err != nil && err != io.EOF {
			t.Fatalf("Unexpected error: %v", err)
		}

		if index != chunkCount {
			t.Errorf("Expected chunk index %d, got %d", chunkCount, index)
		}

		if len(chunk) != 1024*1024 {
			t.Errorf("Chunk %d: expected 1MB, got %d bytes", index, len(chunk))
		}

		reconstructed.Write(chunk)
		chunkCount++
	}

	if chunkCount != 3 {
		t.Errorf("Expected 3 chunks, got %d", chunkCount)
	}

	if !bytes.Equal(testData, reconstructed.Bytes()) {
		t.Error("Reconstructed data doesn't match original")
	}

	t.Logf("✓ Exact size: %d MB → %d chunks", testSize/(1024*1024), chunkCount)
}

// TestChunkReaderSmallFile tests file smaller than chunk size
func TestChunkReaderSmallFile(t *testing.T) {
	testSize := 100 * 1024 // 100KB (smaller than 1MB chunk)
	testData := make([]byte, testSize)
	rand.Read(testData)

	reader := bytes.NewReader(testData)
	chunkReader := NewChunkReader(reader, 1024*1024) // 1MB chunks

	chunk, index, err := chunkReader.NextChunk()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if index != 0 {
		t.Errorf("Expected chunk index 0, got %d", index)
	}

	if len(chunk) != testSize {
		t.Errorf("Expected %d bytes, got %d", testSize, len(chunk))
	}

	if !bytes.Equal(testData, chunk) {
		t.Error("Chunk data doesn't match original")
	}

	// Second call should return EOF
	chunk2, _, err2 := chunkReader.NextChunk()
	if err2 != io.EOF || chunk2 != nil {
		t.Error("Expected EOF on second call")
	}

	t.Logf("✓ Small file: %d KB → 1 chunk", testSize/1024)
}

// TestChunkReaderEmptyFile tests empty file
func TestChunkReaderEmptyFile(t *testing.T) {
	reader := bytes.NewReader([]byte{})
	chunkReader := NewChunkReader(reader, 1024*1024)

	chunk, index, err := chunkReader.NextChunk()
	if err != io.EOF {
		t.Errorf("Expected EOF, got %v", err)
	}
	if chunk != nil {
		t.Error("Expected nil chunk for empty file")
	}
	if index != -1 {
		t.Errorf("Expected index -1, got %d", index)
	}

	t.Log("✓ Empty file handled correctly")
}

// TestChunkReaderLargeFile tests memory efficiency with large file
func TestChunkReaderLargeFile(t *testing.T) {
	// Simulate 100MB file
	testSize := int64(100 * 1024 * 1024)
	chunkSize := 1024 * 1024 // 1MB

	// Create a deterministic reader (doesn't allocate 100MB)
	reader := io.LimitReader(&deterministicReader{}, testSize)
	chunkReader := NewChunkReader(reader, chunkSize)

	chunkCount := 0
	totalBytes := int64(0)

	for {
		chunk, index, err := chunkReader.NextChunk()
		if err == io.EOF && chunk == nil {
			break
		}

		if err != nil && err != io.EOF {
			t.Fatalf("Unexpected error: %v", err)
		}

		if index != chunkCount {
			t.Errorf("Expected chunk index %d, got %d", chunkCount, index)
		}

		totalBytes += int64(len(chunk))
		chunkCount++
	}

	expectedChunks := int(testSize / int64(chunkSize))
	if chunkCount != expectedChunks {
		t.Errorf("Expected %d chunks, got %d", expectedChunks, chunkCount)
	}

	if totalBytes != testSize {
		t.Errorf("Expected %d total bytes, got %d", testSize, totalBytes)
	}

	t.Logf("✓ Large file: %d MB → %d chunks (memory efficient)", testSize/(1024*1024), chunkCount)
}

// deterministicReader generates predictable data on the fly
type deterministicReader struct {
	counter byte
}

func (d *deterministicReader) Read(p []byte) (n int, err error) {
	for i := range p {
		p[i] = d.counter
		d.counter++
	}
	return len(p), nil
}

// BenchmarkChunkReaderThroughput benchmarks chunk reading performance
func BenchmarkChunkReaderThroughput(b *testing.B) {
	// 10MB test data
	testData := make([]byte, 10*1024*1024)
	rand.Read(testData)

	b.ResetTimer()
	b.SetBytes(int64(len(testData)))

	for i := 0; i < b.N; i++ {
		reader := bytes.NewReader(testData)
		chunkReader := NewChunkReader(reader, 1024*1024)

		for {
			chunk, _, err := chunkReader.NextChunk()
			if err == io.EOF && chunk == nil {
				break
			}
		}
	}
}

// BenchmarkChunkReaderAllocation benchmarks memory allocation
func BenchmarkChunkReaderAllocation(b *testing.B) {
	testData := make([]byte, 1024*1024) // 1MB
	rand.Read(testData)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		reader := bytes.NewReader(testData)
		chunkReader := NewChunkReader(reader, 256*1024) // 256KB chunks

		for {
			chunk, _, err := chunkReader.NextChunk()
			if err == io.EOF && chunk == nil {
				break
			}
			_ = chunk // Use chunk to prevent optimization
		}
	}
}
