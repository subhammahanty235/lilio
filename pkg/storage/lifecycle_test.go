package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/subhammahanty235/lilio/pkg/metadata"
)

// chunkIDsOnBackends returns every chunk ID currently held by any backend,
// so a test can assert about what is actually on storage rather than about
// what the metadata claims.
func chunkIDsOnBackends(t *testing.T, lilio *Lilio) map[string]int {
	t.Helper()

	held := make(map[string]int)
	for _, backend := range lilio.Registry.List() {
		ids, err := backend.ListChunks(context.Background())
		if err != nil {
			t.Fatalf("ListChunks on %s: %v", backend.Info().Name, err)
		}
		for _, id := range ids {
			held[id]++
		}
	}
	return held
}

// Integration test: DeleteObject must remove both the metadata and the chunks.
// Before DELETE was routed, this whole path was unreachable from the API and
// the chunks were never reclaimed.
func TestDeleteObjectRemovesChunksAndMetadata(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)

	data := bytes.Repeat([]byte("delete me "), 300) // spans several chunks
	meta, err := lilio.PutObject(context.Background(), "test-bucket", "doomed", bytes.NewReader(data), int64(len(data)), "text/plain")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	if len(chunkIDsOnBackends(t, lilio)) == 0 {
		t.Fatal("Expected chunks on backends after put")
	}

	if err := lilio.DeleteObject(context.Background(), "test-bucket", "doomed"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if _, err := lilio.Metadata.GetObjectMetadata("test-bucket", "doomed"); !errors.Is(err, metadata.ErrObjectNotFound) {
		t.Errorf("Metadata after delete: got %v, want ErrObjectNotFound", err)
	}

	held := chunkIDsOnBackends(t, lilio)
	for _, chunk := range meta.Chunks {
		if held[chunk.ChunkID] > 0 {
			t.Errorf("Chunk %s still on %d backend(s) after delete", chunk.ChunkID, held[chunk.ChunkID])
		}
	}
	if len(held) != 0 {
		t.Errorf("Expected no chunks left on any backend, found %d", len(held))
	}
}

// Integration test: DELETE has to be idempotent. A client whose request times
// out will retry, and the retry must not report failure for work the first
// attempt already completed.
func TestDeleteObjectIsIdempotent(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)

	if err := lilio.DeleteObject(context.Background(), "test-bucket", "never-existed"); err != nil {
		t.Errorf("Deleting a missing object should succeed, got: %v", err)
	}

	data := []byte("transient")
	if _, err := lilio.PutObject(context.Background(), "test-bucket", "twice", bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	if err := lilio.DeleteObject(context.Background(), "test-bucket", "twice"); err != nil {
		t.Fatalf("First delete failed: %v", err)
	}
	if err := lilio.DeleteObject(context.Background(), "test-bucket", "twice"); err != nil {
		t.Errorf("Second delete should succeed, got: %v", err)
	}
}

// Integration test: overwriting a key must reclaim the chunks of the object it
// replaced. Each PutObject mints a fresh object ID and therefore a fresh set of
// chunk IDs, so without explicit reclamation the previous object's chunks stay
// on disk forever, referenced by nothing.
func TestOverwriteReclaimsSupersededChunks(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)

	first := bytes.Repeat([]byte("first version "), 200)
	firstMeta, err := lilio.PutObject(context.Background(), "test-bucket", "doc", bytes.NewReader(first), int64(len(first)), "text/plain")
	if err != nil {
		t.Fatalf("First put failed: %v", err)
	}

	second := bytes.Repeat([]byte("second version "), 200)
	secondMeta, err := lilio.PutObject(context.Background(), "test-bucket", "doc", bytes.NewReader(second), int64(len(second)), "text/plain")
	if err != nil {
		t.Fatalf("Second put failed: %v", err)
	}

	held := chunkIDsOnBackends(t, lilio)

	for _, chunk := range firstMeta.Chunks {
		if held[chunk.ChunkID] > 0 {
			t.Errorf("Superseded chunk %s still on %d backend(s)", chunk.ChunkID, held[chunk.ChunkID])
		}
	}
	for _, chunk := range secondMeta.Chunks {
		if held[chunk.ChunkID] == 0 {
			t.Errorf("Current chunk %s missing from every backend", chunk.ChunkID)
		}
	}

	// The object must still read back as the new version.
	var buf bytes.Buffer
	if err := lilio.GetObject(context.Background(), "test-bucket", "doc", &buf); err != nil {
		t.Fatalf("Read after overwrite failed: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), second) {
		t.Error("Read after overwrite returned the wrong version")
	}
}

// Failure test: a PUT that cannot meet its write quorum must not leave metadata
// behind. The chunks it already wrote do leak - that is the deliberate
// trade-off, since leaked storage can be swept up later while metadata pointing
// at absent chunks could not be recovered at all - but the object must not
// become visible.
func TestFailedWriteLeavesNoMetadata(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 1) // only 1 backend, W=2 cannot be met

	data := []byte("doomed write")
	if _, err := lilio.PutObject(context.Background(), "test-bucket", "partial", bytes.NewReader(data), int64(len(data)), "text/plain"); err == nil {
		t.Fatal("Expected the write to fail when quorum cannot be met")
	}

	if _, err := lilio.Metadata.GetObjectMetadata("test-bucket", "partial"); !errors.Is(err, metadata.ErrObjectNotFound) {
		t.Errorf("A failed write must not publish metadata: got %v, want ErrObjectNotFound", err)
	}
}

// Failure test: an overwrite that fails must leave the previous version intact
// and readable. This is the case that would break if superseded chunks were
// reclaimed before the new metadata committed.
func TestFailedOverwriteLeavesPreviousVersionReadable(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)

	original := []byte("the version that must survive")
	if _, err := lilio.PutObject(context.Background(), "test-bucket", "doc", bytes.NewReader(original), int64(len(original)), "text/plain"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Take two backends offline, keeping the instances so they can be brought
	// back with their data - a node returning from an outage, not a blank one.
	offline := make([]StorageBackend, 0, 2)
	for _, name := range []string{"mock-backend-1", "mock-backend-2"} {
		backend, err := lilio.Registry.Get(name)
		if err != nil {
			t.Fatalf("Setup failed: %v", err)
		}
		offline = append(offline, backend)
		if err := lilio.RemoveBackend(name); err != nil {
			t.Fatalf("Setup failed: %v", err)
		}
	}

	replacement := []byte("this write will not succeed")
	if _, err := lilio.PutObject(context.Background(), "test-bucket", "doc", bytes.NewReader(replacement), int64(len(replacement)), "text/plain"); err == nil {
		t.Fatal("Expected the overwrite to fail when quorum cannot be met")
	}

	// Bring them back and confirm the original is still there, whole.
	for _, backend := range offline {
		if err := lilio.AddBackend(backend); err != nil {
			t.Fatalf("Failed to restore backend: %v", err)
		}
	}

	var buf bytes.Buffer
	if err := lilio.GetObject(context.Background(), "test-bucket", "doc", &buf); err != nil {
		t.Fatalf("Original should still be readable after a failed overwrite: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), original) {
		t.Errorf("Original was damaged by a failed overwrite: got %q", buf.Bytes())
	}
}
