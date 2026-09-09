package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/subhammahanty235/lilio/pkg/metadata"
)

// TestQuorumWriteSuccess tests that writes succeed when quorum is met
func TestQuorumWriteSuccess(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2) // N=3, W=2, R=2
	defer cleanup(lilio)

	// Add 3 backends
	addMockBackends(lilio, 3)

	// Write should succeed (all 3 backends online, W=2)
	data := []byte("test data for quorum write")
	_, err := lilio.PutObject(context.Background(), "test-bucket", "test-key", bytes.NewReader(data), int64(len(data)), "text/plain")
	if err != nil {
		t.Fatalf("Write should succeed with 3/3 nodes and W=2: %v", err)
	}

	t.Log("✓ Write quorum succeeded with 3/3 nodes")
}

// TestQuorumWriteFailure tests that writes fail when quorum not met
func TestQuorumWriteFailure(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2) // N=3, W=2, R=2
	defer cleanup(lilio)

	// Add only 1 backend (insufficient for W=2)
	addMockBackends(lilio, 1)

	data := []byte("test data")
	_, err := lilio.PutObject(context.Background(), "test-bucket", "test-key", bytes.NewReader(data), int64(len(data)), "text/plain")
	if err == nil {
		t.Fatal("Write should fail with only 1/2 required nodes")
	}

	if !contains(err.Error(), "write quorum failed") {
		t.Errorf("Expected 'write quorum failed' error, got: %v", err)
	}

	t.Log("✓ Write correctly failed when quorum not met")
}

// TestQuorumReadSuccess tests that reads succeed when quorum met
func TestQuorumReadSuccess(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2)
	defer cleanup(lilio)

	addMockBackends(lilio, 3)

	// Write data
	data := []byte("read quorum test data")
	_, err := lilio.PutObject(context.Background(), "test-bucket", "read-key", bytes.NewReader(data), int64(len(data)), "text/plain")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Read should succeed (3 backends available, R=2)
	var buf bytes.Buffer
	err = lilio.GetObject(context.Background(), "test-bucket", "read-key", &buf)
	if err != nil {
		t.Fatalf("Read should succeed with 3/3 nodes and R=2: %v", err)
	}

	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("Data mismatch. Expected %s, got %s", data, buf.Bytes())
	}

	t.Log("✓ Read quorum succeeded with 3/3 nodes")
}

// TestReadSucceedsFromLastSurvivingReplica is the behaviour a read quorum used
// to prevent. Two of three nodes are gone; the third holds a copy whose
// checksum matches the metadata. That copy is provably the right data, so
// refusing to serve it - as W+R>N required - cost availability for nothing.
func TestReadSucceedsFromLastSurvivingReplica(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)

	data := []byte("one good copy is proof enough")
	if _, err := lilio.PutObject(context.Background(), "test-bucket", "survivor", bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	lilio.RemoveBackend("mock-backend-1")
	lilio.RemoveBackend("mock-backend-2")

	var buf bytes.Buffer
	if err := lilio.GetObject(context.Background(), "test-bucket", "survivor", &buf); err != nil {
		t.Fatalf("Read should succeed from the one intact replica: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("Got %q, want %q", buf.Bytes(), data)
	}
}

// TestReadFailsWhenNoReplicaIsIntact: a read may only fail when no replica can
// produce bytes matching the checksum - not merely because too few answered.
func TestReadFailsWhenNoReplicaIsIntact(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)

	data := []byte("every copy will be ruined")
	meta, err := lilio.PutObject(context.Background(), "test-bucket", "ruined", bytes.NewReader(data), int64(len(data)), "text/plain")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	chunkID := meta.Chunks[0].ChunkID
	for _, name := range []string{"mock-backend-0", "mock-backend-1", "mock-backend-2"} {
		backend, _ := lilio.Registry.Get(name)
		backend.StoreChunk(context.Background(), chunkID, []byte("corrupted"))
	}

	var buf bytes.Buffer
	if err := lilio.GetObject(context.Background(), "test-bucket", "ruined", &buf); err == nil {
		t.Fatal("Expected the read to fail when no replica matches the checksum")
	}
}

// TestReadRepairsCorruptReplicaItPassed: a replica that answers with the wrong
// bytes on the way to a good one still gets fixed.
func TestReadRepairsCorruptReplicaItPassed(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)

	data := []byte("one replica will be rotten")
	meta, err := lilio.PutObject(context.Background(), "test-bucket", "rotten", bytes.NewReader(data), int64(len(data)), "text/plain")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	chunkID := meta.Chunks[0].ChunkID
	first := lilio.orderReplicas(meta.Chunks[0].StorageNodes)[0]
	backend, _ := lilio.Registry.Get(first)
	backend.StoreChunk(context.Background(), chunkID, []byte("corrupted"))

	var buf bytes.Buffer
	if err := lilio.GetObject(context.Background(), "test-bucket", "rotten", &buf); err != nil {
		t.Fatalf("Read should have fallen through to a good replica: %v", err)
	}

	time.Sleep(200 * time.Millisecond) // repair is asynchronous
	repaired, err := backend.RetrieveChunk(context.Background(), chunkID)
	if err != nil {
		t.Fatalf("Could not read the repaired replica: %v", err)
	}
	if CalculateChecksum(repaired) != meta.Chunks[0].Checksum {
		t.Error("The corrupt replica the read passed over was not repaired")
	}
}

// Read repair is now covered by TestReadRepairsCorruptReplicaItPassed above.
// The original test here corrupted an arbitrary replica and expected a read to
// fix it, which assumed every read contacted every replica. Reads now stop at
// the first copy matching the checksum, so a corrupt replica the read never
// reached is not repaired by it - that case belongs to the scrubber, which
// checks every replica of every chunk whether or not anyone is reading it.

// TestInvalidQuorumConfig tests validation of the replication policy
func TestInvalidQuorumConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  QuorumConfig
		want string
	}{
		{
			name: "W above N can never be met",
			cfg:  QuorumConfig{N: 3, W: 4},
			want: "invalid write quorum",
		},
		{
			name: "W below 1 would commit a write nobody accepted",
			cfg:  QuorumConfig{N: 3, W: 0},
			want: "invalid write quorum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			cfg := Config{
				BasePath:          tempDir,
				ChunkSize:         1024,
				ReplicationFactor: tt.cfg.N,
				Quorum:            &tt.cfg,
				MetadataConfig: &metadata.Config{
					Type: metadata.StoreTypeMemory,
				},
			}

			_, err := NewLilioInstance(cfg)
			if err == nil {
				t.Errorf("Expected error for invalid quorum config")
			}
			if !contains(err.Error(), tt.want) {
				t.Errorf("Expected error containing '%s', got: %v", tt.want, err)
			}
		})
	}

	t.Log("✓ Invalid replication configurations correctly rejected")
}

// TestDefaultQuorum verifies default quorum calculation
func TestDefaultQuorum(t *testing.T) {
	tests := []struct {
		rf    int
		wantW int
	}{
		{rf: 3, wantW: 2}, // a majority of 3
		{rf: 5, wantW: 3}, // a majority of 5
		{rf: 1, wantW: 1}, // a single copy must still be written
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("RF=%d", tt.rf), func(t *testing.T) {
			q := DefaultQuorum(tt.rf)

			if q.N != tt.rf || q.W != tt.wantW {
				t.Errorf("DefaultQuorum(%d) = N:%d W:%d; want N:%d W:%d",
					tt.rf, q.N, q.W, tt.rf, tt.wantW)
			}
			if q.W > q.N {
				t.Errorf("DefaultQuorum(%d) produced an unreachable W: %d > %d", tt.rf, q.W, q.N)
			}
		})
	}

	t.Log("✓ Default quorum calculation verified")
}

// Helper functions

func setupTestLilio(t *testing.T, n, w int) *Lilio {
	tempDir := t.TempDir()
	cfg := Config{
		BasePath:          tempDir,
		ChunkSize:         1024,
		ReplicationFactor: n,
		Quorum:            &QuorumConfig{N: n, W: w},
		MetadataConfig: &metadata.Config{
			Type: metadata.StoreTypeMemory,
		},
	}

	lilio, err := NewLilioInstance(cfg)
	if err != nil {
		t.Fatalf("Failed to create Lilio instance: %v", err)
	}

	// Create test bucket
	if err := lilio.CreateBucket("test-bucket"); err != nil {
		t.Fatalf("Failed to create test bucket: %v", err)
	}

	return lilio
}

func addMockBackends(lilio *Lilio, count int) {
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("mock-backend-%d", i)
		backend := &MockBackend{
			name:   name,
			chunks: make(map[string][]byte),
		}
		lilio.AddBackend(backend)
	}
}

func cleanup(lilio *Lilio) {
	if lilio.Metadata != nil {
		lilio.Metadata.Close()
	}
	// Clean up temp directories
	os.RemoveAll(lilio.BasePath)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// MockBackend for testing.
//
// It is mutex-guarded because a real backend is: writes fan out to replicas
// concurrently, and read repair and chunk reclamation touch backends from
// background goroutines after the request that started them has returned.
type MockBackend struct {
	name   string
	mu     sync.RWMutex
	chunks map[string][]byte
}

func (m *MockBackend) StoreChunk(ctx context.Context, chunkID string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunks[chunkID] = data
	return nil
}

func (m *MockBackend) RetrieveChunk(ctx context.Context, chunkID string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, exists := m.chunks[chunkID]
	if !exists {
		return nil, fmt.Errorf("chunk not found: %s", chunkID)
	}
	return data, nil
}

func (m *MockBackend) DeleteChunk(ctx context.Context, chunkID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.chunks, chunkID)
	return nil
}

func (m *MockBackend) Info() BackendInfo {
	return BackendInfo{
		Name:     m.name,
		Type:     "mock",
		Status:   StatusOnline,
		Priority: 1,
	}
}

func (m *MockBackend) Stats(ctx context.Context) (BackendStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return BackendStats{
		ChunksStored: int64(len(m.chunks)),
		BytesUsed:    0,
	}, nil
}

func (m *MockBackend) Health(ctx context.Context) error {
	return nil
}

func (m *MockBackend) HasChunk(ctx context.Context, chunkID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.chunks[chunkID]
	return exists
}

func (m *MockBackend) ListChunks(ctx context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var chunks []string
	for id := range m.chunks {
		chunks = append(chunks, id)
	}
	return chunks, nil
}
