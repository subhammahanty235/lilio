package metadata

import (
	"fmt"
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// MetadataStoreTestSuite runs comprehensive tests against any MetadataStore implementation
// This ensures all backends (local, etcd, memory) behave identically
type MetadataStoreTestSuite struct {
	Store MetadataStore
	t     *testing.T
}

func NewTestSuite(t *testing.T, store MetadataStore) *MetadataStoreTestSuite {
	return &MetadataStoreTestSuite{
		Store: store,
		t:     t,
	}
}

// TestBucketLifecycle tests create, get, list, delete operations
func (s *MetadataStoreTestSuite) TestBucketLifecycle() {
	t := s.t
	store := s.Store

	// 1. Create bucket
	err := store.CreateBucket("test-bucket")
	if err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// 2. Bucket should exist
	if !store.BucketExists("test-bucket") {
		t.Error("Bucket should exist after creation")
	}

	// 3. Duplicate create should fail
	err = store.CreateBucket("test-bucket")
	if err == nil {
		t.Error("Creating duplicate bucket should fail")
	}

	// 4. Get bucket metadata
	bucket, err := store.GetBucket("test-bucket")
	if err != nil {
		t.Fatalf("Failed to get bucket: %v", err)
	}
	if bucket.Name != "test-bucket" {
		t.Errorf("Expected name 'test-bucket', got '%s'", bucket.Name)
	}
	if bucket.Encryption.Enabled {
		t.Error("Bucket should not be encrypted by default")
	}

	// 5. List buckets
	buckets, err := store.ListBuckets()
	if err != nil {
		t.Fatalf("Failed to list buckets: %v", err)
	}
	found := false
	for _, name := range buckets {
		if name == "test-bucket" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Created bucket not found in list")
	}

	// 6. Delete non-empty bucket should fail (create object first)
	meta := &ObjectMetadata{
		ObjectID:    "obj-1",
		Bucket:      "test-bucket",
		Key:         "test-key",
		Size:        100,
		Checksum:    "abc123",
		ChunkSize:   1024,
		TotalChunks: 1,
		Chunks:      []ChunkInfo{},
		CreatedAt:   time.Now(),
		ContentType: "text/plain",
	}
	store.SaveObjectMetadata(meta)

	err = store.DeleteBucket("test-bucket")
	if err == nil {
		t.Error("Deleting non-empty bucket should fail")
	}

	// 7. Delete object then bucket
	store.DeleteObjectMetadata("test-bucket", "test-key")
	err = store.DeleteBucket("test-bucket")
	if err != nil {
		t.Errorf("Failed to delete empty bucket: %v", err)
	}

	// 8. Bucket should not exist
	if store.BucketExists("test-bucket") {
		t.Error("Bucket should not exist after deletion")
	}

	t.Logf("✓ Bucket lifecycle tests passed for %s backend", store.Type())
}

// TestBucketEncryption tests encrypted bucket creation
func (s *MetadataStoreTestSuite) TestBucketEncryption() {
	t := s.t
	store := s.Store

	// Create encrypted bucket
	encConfig := EncryptionConfig{
		Enabled:   true,
		Algorithm: "aes256-gcm",
		Salt:      []byte("test-salt-12345"),
		KeyHash:   "testhash123",
	}

	err := store.CreateBucketWithEncryption("encrypted-bucket", encConfig)
	if err != nil {
		t.Fatalf("Failed to create encrypted bucket: %v", err)
	}

	// Verify encryption settings
	isEncrypted, err := store.IsBucketEncrypted("encrypted-bucket")
	if err != nil {
		t.Fatalf("Failed to check encryption: %v", err)
	}
	if !isEncrypted {
		t.Error("Bucket should be marked as encrypted")
	}

	// Get encryption config
	enc, err := store.GetBucketEncryption("encrypted-bucket")
	if err != nil {
		t.Fatalf("Failed to get encryption config: %v", err)
	}
	if enc.Algorithm != "aes256-gcm" {
		t.Errorf("Expected algorithm 'aes256-gcm', got '%s'", enc.Algorithm)
	}
	if enc.KeyHash != "testhash123" {
		t.Errorf("Expected key hash 'testhash123', got '%s'", enc.KeyHash)
	}

	// Cleanup
	store.DeleteBucket("encrypted-bucket")

	t.Logf("✓ Bucket encryption tests passed for %s backend", store.Type())
}

// TestObjectLifecycle tests object metadata operations
func (s *MetadataStoreTestSuite) TestObjectLifecycle() {
	t := s.t
	store := s.Store

	// Setup: Create bucket
	store.CreateBucket("obj-bucket")

	// 1. Save object metadata
	meta := &ObjectMetadata{
		ObjectID:    "obj-123",
		Bucket:      "obj-bucket",
		Key:         "photos/vacation.jpg",
		Size:        1024000,
		Checksum:    "abc123def456",
		ChunkSize:   1048576,
		TotalChunks: 2,
		Chunks: []ChunkInfo{
			{
				ChunkID:      "obj-123_chunk_0",
				ChunkIndex:   0,
				Size:         1048576,
				Checksum:     "chunk0hash",
				StorageNodes: []string{"node-1", "node-2"},
			},
			{
				ChunkID:      "obj-123_chunk_1",
				ChunkIndex:   1,
				Size:         100,
				Checksum:     "chunk1hash",
				StorageNodes: []string{"node-2", "node-3"},
			},
		},
		CreatedAt:   time.Now(),
		ContentType: "image/jpeg",
		Encrypted:   false,
	}

	err := store.SaveObjectMetadata(meta)
	if err != nil {
		t.Fatalf("Failed to save object metadata: %v", err)
	}

	// 2. Get object metadata
	retrieved, err := store.GetObjectMetadata("obj-bucket", "photos/vacation.jpg")
	if err != nil {
		t.Fatalf("Failed to get object metadata: %v", err)
	}

	if retrieved.ObjectID != "obj-123" {
		t.Errorf("Expected ObjectID 'obj-123', got '%s'", retrieved.ObjectID)
	}
	if retrieved.Size != 1024000 {
		t.Errorf("Expected size 1024000, got %d", retrieved.Size)
	}
	if len(retrieved.Chunks) != 2 {
		t.Errorf("Expected 2 chunks, got %d", len(retrieved.Chunks))
	}
	if retrieved.Chunks[0].ChunkID != "obj-123_chunk_0" {
		t.Errorf("Chunk 0 ID mismatch: %s", retrieved.Chunks[0].ChunkID)
	}

	// 3. List objects
	objects, err := store.ListObjects("obj-bucket", "")
	if err != nil {
		t.Fatalf("Failed to list objects: %v", err)
	}
	if len(objects) != 1 {
		t.Errorf("Expected 1 object, got %d", len(objects))
	}
	if objects[0] != "photos/vacation.jpg" {
		t.Errorf("Expected 'photos/vacation.jpg', got '%s'", objects[0])
	}

	// 4. List with prefix
	store.SaveObjectMetadata(&ObjectMetadata{
		ObjectID: "obj-124",
		Bucket:   "obj-bucket",
		Key:      "photos/beach.jpg",
		Size:     500,
	})
	store.SaveObjectMetadata(&ObjectMetadata{
		ObjectID: "obj-125",
		Bucket:   "obj-bucket",
		Key:      "documents/report.pdf",
		Size:     1000,
	})

	photosOnly, err := store.ListObjects("obj-bucket", "photos/")
	if err != nil {
		t.Fatalf("Failed to list with prefix: %v", err)
	}
	if len(photosOnly) != 2 {
		t.Errorf("Expected 2 photos, got %d", len(photosOnly))
	}

	// 5. Update object metadata (overwrite)
	meta.Size = 2048000
	err = store.SaveObjectMetadata(meta)
	if err != nil {
		t.Fatalf("Failed to update metadata: %v", err)
	}

	updated, _ := store.GetObjectMetadata("obj-bucket", "photos/vacation.jpg")
	if updated.Size != 2048000 {
		t.Errorf("Update failed: expected size 2048000, got %d", updated.Size)
	}

	// 6. Delete object
	err = store.DeleteObjectMetadata("obj-bucket", "photos/vacation.jpg")
	if err != nil {
		t.Fatalf("Failed to delete object: %v", err)
	}

	_, err = store.GetObjectMetadata("obj-bucket", "photos/vacation.jpg")
	if err == nil {
		t.Error("Getting deleted object should fail")
	}

	// Cleanup
	store.DeleteObjectMetadata("obj-bucket", "photos/beach.jpg")
	store.DeleteObjectMetadata("obj-bucket", "documents/report.pdf")
	store.DeleteBucket("obj-bucket")

	t.Logf("✓ Object lifecycle tests passed for %s backend", store.Type())
}

// TestConcurrency tests thread safety
func (s *MetadataStoreTestSuite) TestConcurrency() {
	t := s.t
	store := s.Store

	store.CreateBucket("concurrent-bucket")

	// Concurrent writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			meta := &ObjectMetadata{
				ObjectID: fmt.Sprintf("obj-%d", id),
				Bucket:   "concurrent-bucket",
				Key:      fmt.Sprintf("key-%d", id),
				Size:     int64(id * 100),
			}
			store.SaveObjectMetadata(meta)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all objects saved
	objects, _ := store.ListObjects("concurrent-bucket", "")
	if len(objects) != 10 {
		t.Errorf("Expected 10 objects after concurrent writes, got %d", len(objects))
	}

	// Cleanup
	for i := 0; i < 10; i++ {
		store.DeleteObjectMetadata("concurrent-bucket", fmt.Sprintf("key-%d", i))
	}
	store.DeleteBucket("concurrent-bucket")

	t.Logf("✓ Concurrency tests passed for %s backend", store.Type())
}

// TestHealth tests health check
func (s *MetadataStoreTestSuite) TestHealth() {
	t := s.t
	store := s.Store

	err := store.Health()
	if err != nil {
		t.Errorf("Health check failed: %v", err)
	}

	t.Logf("✓ Health check passed for %s backend", store.Type())
}

// Run all tests in the suite
func (s *MetadataStoreTestSuite) RunAll() {
	s.TestHealth()
	s.TestBucketLifecycle()
	s.TestBucketEncryption()
	s.TestObjectLifecycle()
	s.TestConcurrency()
}

// Test LocalStore
func TestLocalStore(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("Failed to create local store: %v", err)
	}
	defer store.Close()

	suite := NewTestSuite(t, store)
	suite.RunAll()
}

// Test MemoryStore
func TestMemoryStore(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	suite := NewTestSuite(t, store)
	suite.RunAll()
}

// Test etcd Store (requires etcd running)
func TestEtcdStore(t *testing.T) {
	// Skip if etcd not available
	cfg := &EtcdConfig{
		Endpoints: []string{"localhost:2379"},
		Prefix:    "/lilio-test",
	}

	store, err := NewEtcdStore(cfg)
	if err != nil {
		t.Skipf("etcd not available: %v", err)
		return
	}
	defer store.Close()

	// Clean up test data
	defer func() {
		// Delete all test keys
		store.client.Delete(store.client.Ctx(), "/lilio-test/", clientv3.WithPrefix())
	}()

	suite := NewTestSuite(t, store)
	suite.RunAll()
}

// Test factory function
func TestFactory(t *testing.T) {
	// Test local store creation via factory
	store, err := NewStore(Config{
		Type: StoreTypeLocal,
		LocalConfig: &LocalConfig{
			Path: t.TempDir(),
		},
	})
	if err != nil {
		t.Fatalf("Factory failed for local: %v", err)
	}
	if store.Type() != "local" {
		t.Errorf("Expected type 'local', got '%s'", store.Type())
	}
	store.Close()

	// Test memory store creation via factory
	memStore, err := NewStore(Config{
		Type: StoreTypeMemory,
	})
	if err != nil {
		t.Fatalf("Factory failed for memory: %v", err)
	}
	if memStore.Type() != "memory" {
		t.Errorf("Expected type 'memory', got '%s'", memStore.Type())
	}
	memStore.Close()

	t.Log("✓ Factory tests passed")
}

// Benchmark tests
func BenchmarkLocalStore_SaveObject(b *testing.B) {
	store, _ := NewLocalStore(b.TempDir())
	defer store.Close()
	store.CreateBucket("bench-bucket")

	meta := &ObjectMetadata{
		ObjectID:    "bench-obj",
		Bucket:      "bench-bucket",
		Key:         "bench-key",
		Size:        1024,
		Checksum:    "benchhash",
		ChunkSize:   1024,
		TotalChunks: 1,
		Chunks:      []ChunkInfo{},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.SaveObjectMetadata(meta)
	}
}

func BenchmarkMemoryStore_SaveObject(b *testing.B) {
	store := NewMemoryStore()
	defer store.Close()
	store.CreateBucket("bench-bucket")

	meta := &ObjectMetadata{
		ObjectID:    "bench-obj",
		Bucket:      "bench-bucket",
		Key:         "bench-key",
		Size:        1024,
		Checksum:    "benchhash",
		ChunkSize:   1024,
		TotalChunks: 1,
		Chunks:      []ChunkInfo{},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.SaveObjectMetadata(meta)
	}
}
