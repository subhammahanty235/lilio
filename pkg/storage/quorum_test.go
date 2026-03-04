package storage

import (
	"bytes"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/subhammahanty235/lilio/pkg/metadata"
)

// TestQuorumWriteSuccess tests that writes succeed when quorum is met
func TestQuorumWriteSuccess(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2, 2) // N=3, W=2, R=2
	defer cleanup(lilio)

	// Add 3 backends
	addMockBackends(lilio, 3)

	// Write should succeed (all 3 backends online, W=2)
	data := []byte("test data for quorum write")
	_, err := lilio.PutObject("test-bucket", "test-key", bytes.NewReader(data), int64(len(data)), "text/plain")
	if err != nil {
		t.Fatalf("Write should succeed with 3/3 nodes and W=2: %v", err)
	}

	t.Log("✓ Write quorum succeeded with 3/3 nodes")
}

// TestQuorumWriteFailure tests that writes fail when quorum not met
func TestQuorumWriteFailure(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2, 2) // N=3, W=2, R=2
	defer cleanup(lilio)

	// Add only 1 backend (insufficient for W=2)
	addMockBackends(lilio, 1)

	data := []byte("test data")
	_, err := lilio.PutObject("test-bucket", "test-key", bytes.NewReader(data), int64(len(data)), "text/plain")
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
	lilio := setupTestLilio(t, 3, 2, 2)
	defer cleanup(lilio)

	addMockBackends(lilio, 3)

	// Write data
	data := []byte("read quorum test data")
	_, err := lilio.PutObject("test-bucket", "read-key", bytes.NewReader(data), int64(len(data)), "text/plain")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Read should succeed (3 backends available, R=2)
	var buf bytes.Buffer
	err = lilio.GetObject("test-bucket", "read-key", &buf)
	if err != nil {
		t.Fatalf("Read should succeed with 3/3 nodes and R=2: %v", err)
	}

	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("Data mismatch. Expected %s, got %s", data, buf.Bytes())
	}

	t.Log("✓ Read quorum succeeded with 3/3 nodes")
}

// TestQuorumReadFailure tests that reads fail when quorum not met
func TestQuorumReadFailure(t *testing.T) {
	lilio := setupTestLilio(t, 3, 3, 3) // N=3, W=3, R=3 (requires all nodes)
	defer cleanup(lilio)

	addMockBackends(lilio, 3)

	// Write data with all 3 nodes
	data := []byte("strict quorum test")
	_, err := lilio.PutObject("test-bucket", "strict-key", bytes.NewReader(data), int64(len(data)), "text/plain")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Remove 1 backend to simulate failure
	lilio.RemoveBackend("mock-backend-2")

	// Read should fail (only 2/3 nodes, R=3 requires all)
	var buf bytes.Buffer
	err = lilio.GetObject("test-bucket", "strict-key", &buf)
	if err == nil {
		t.Fatal("Read should fail with only 2/3 nodes when R=3")
	}

	if !contains(err.Error(), "read quorum failed") {
		t.Errorf("Expected 'read quorum failed' error, got: %v", err)
	}

	t.Log("✓ Read correctly failed when quorum not met")
}

// TestReadRepair tests that read repair fixes stale replicas
func TestReadRepair(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2, 2)
	defer cleanup(lilio)

	addMockBackends(lilio, 3)

	// Write initial data
	data := []byte("original data")
	meta, err := lilio.PutObject("test-bucket", "repair-key", bytes.NewReader(data), int64(len(data)), "text/plain")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Simulate corruption on one backend by overwriting chunk with bad data
	backend, _ := lilio.Registry.Get("mock-backend-1")
	chunkID := meta.Chunks[0].ChunkID
	backend.StoreChunk(chunkID, []byte("corrupted data"))

	// Read should trigger read repair
	var buf bytes.Buffer
	err = lilio.GetObject("test-bucket", "repair-key", &buf)
	if err != nil {
		t.Fatalf("Read should succeed and trigger repair: %v", err)
	}

	// Give read repair goroutine time to complete
	time.Sleep(100 * time.Millisecond)

	// Verify repaired data on backend-1
	repairedData, err := backend.RetrieveChunk(chunkID)
	if err != nil {
		t.Fatalf("Failed to retrieve repaired chunk: %v", err)
	}

	if CalculateChecksum(repairedData) != meta.Chunks[0].Checksum {
		t.Error("Read repair did not fix corrupted chunk")
	}

	t.Log("✓ Read repair successfully fixed corrupted replica")
}

// TestInvalidQuorumConfig tests validation of quorum settings
func TestInvalidQuorumConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  QuorumConfig
		want string
	}{
		{
			name: "W+R <= N (allows stale reads)",
			cfg:  QuorumConfig{N: 3, W: 1, R: 2}, // 1+2 = 3, not > 3
			want: "invalid quorum",
		},
		{
			name: "W+R <= N (edge case)",
			cfg:  QuorumConfig{N: 5, W: 2, R: 3}, // 2+3 = 5, not > 5
			want: "invalid quorum",
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

	t.Log("✓ Invalid quorum configurations correctly rejected")
}

// TestDefaultQuorum verifies default quorum calculation
func TestDefaultQuorum(t *testing.T) {
	tests := []struct {
		rf      int
		wantW   int
		wantR   int
		wantErr bool
	}{
		{rf: 3, wantW: 2, wantR: 2, wantErr: false}, // (3/2)+1 = 2, 2+2=4 > 3 ✓
		{rf: 5, wantW: 3, wantR: 3, wantErr: false}, // (5/2)+1 = 3, 3+3=6 > 5 ✓
		{rf: 1, wantW: 1, wantR: 1, wantErr: false}, // (1/2)+1 = 1, 1+1=2 > 1 ✓ (valid)
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("RF=%d", tt.rf), func(t *testing.T) {
			q := DefaultQuorum(tt.rf)

			if q.W != tt.wantW || q.R != tt.wantR {
				t.Errorf("DefaultQuorum(%d) = W:%d, R:%d; want W:%d, R:%d",
					tt.rf, q.W, q.R, tt.wantW, tt.wantR)
			}

			// Verify W+R > N invariant (should always hold for default quorum)
			if q.W+q.R <= q.N {
				t.Errorf("DefaultQuorum(%d) violates W+R > N: %d+%d <= %d",
					tt.rf, q.W, q.R, q.N)
			}
		})
	}

	t.Log("✓ Default quorum calculation verified")
}

// Helper functions

func setupTestLilio(t *testing.T, n, w, r int) *Lilio {
	tempDir := t.TempDir()
	cfg := Config{
		BasePath:          tempDir,
		ChunkSize:         1024,
		ReplicationFactor: n,
		Quorum:            &QuorumConfig{N: n, W: w, R: r},
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

// MockBackend for testing
type MockBackend struct {
	name   string
	chunks map[string][]byte
}

func (m *MockBackend) StoreChunk(chunkID string, data []byte) error {
	m.chunks[chunkID] = data
	return nil
}

func (m *MockBackend) RetrieveChunk(chunkID string) ([]byte, error) {
	data, exists := m.chunks[chunkID]
	if !exists {
		return nil, fmt.Errorf("chunk not found: %s", chunkID)
	}
	return data, nil
}

func (m *MockBackend) DeleteChunk(chunkID string) error {
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

func (m *MockBackend) Stats() (BackendStats, error) {
	return BackendStats{
		ChunksStored: int64(len(m.chunks)),
		BytesUsed:    0,
	}, nil
}

func (m *MockBackend) Health() error {
	return nil
}

func (m *MockBackend) HasChunk(chunkID string) bool {
	_, exists := m.chunks[chunkID]
	return exists
}

func (m *MockBackend) ListChunks() ([]string, error) {
	var chunks []string
	for id := range m.chunks {
		chunks = append(chunks, id)
	}
	return chunks, nil
}
