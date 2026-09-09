package storage

import (
	"bytes"
	"context"
	"testing"
)

// putScrubObject writes a small object and returns its first chunk's ID.
func putScrubObject(t *testing.T, lilio *Lilio, key string, data []byte) string {
	t.Helper()
	meta, err := lilio.PutObject(context.Background(), "test-bucket", key,
		bytes.NewReader(data), int64(len(data)), "text/plain")
	if err != nil {
		t.Fatalf("Setup put failed: %v", err)
	}
	return meta.Chunks[0].ChunkID
}

func mockBackend(t *testing.T, lilio *Lilio, name string) *MockBackend {
	t.Helper()
	backend, err := lilio.Registry.Get(name)
	if err != nil {
		t.Fatalf("Backend %s not found: %v", name, err)
	}
	return backend.(*MockBackend)
}

// A cluster with nothing wrong must scrub clean.
func TestScrubHealthyClusterFindsNothing(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)
	putScrubObject(t, lilio, "intact", []byte("nothing wrong here"))

	report, err := lilio.Scrub(context.Background(), ScrubOptions{})
	if err != nil {
		t.Fatalf("Scrub failed: %v", err)
	}
	if !report.Healthy() {
		t.Errorf("Expected a clean report, got %+v", report.Issues)
	}
	if report.ChunksHealthy != report.ChunksScanned || report.ChunksScanned == 0 {
		t.Errorf("Expected all %d chunks healthy, got %d", report.ChunksScanned, report.ChunksHealthy)
	}
}

// The gap read repair cannot close: a replica that is simply gone. Reading the
// object will never restore it, because a node that returns an error is skipped
// rather than repaired.
func TestScrubRestoresMissingReplica(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)

	chunkID := putScrubObject(t, lilio, "gap", []byte("one replica will vanish"))
	victim := mockBackend(t, lilio, "mock-backend-1")
	victim.DeleteChunk(context.Background(), chunkID)

	// Reading first, to show a read does not fix it.
	var buf bytes.Buffer
	if err := lilio.GetObject(context.Background(), "test-bucket", "gap", &buf); err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if victim.HasChunk(context.Background(), chunkID) {
		t.Fatal("Test premise wrong: a read restored the missing replica")
	}

	report, err := lilio.Scrub(context.Background(), ScrubOptions{})
	if err != nil {
		t.Fatalf("Scrub failed: %v", err)
	}

	if !victim.HasChunk(context.Background(), chunkID) {
		t.Error("Scrub did not restore the missing replica")
	}
	if report.ReplicasRestored != 1 {
		t.Errorf("Expected 1 replica restored, got %d", report.ReplicasRestored)
	}
	if report.ChunksRepaired != 1 {
		t.Errorf("Expected 1 chunk repaired, got %d", report.ChunksRepaired)
	}
}

// Corruption is only visible to a deep pass; a presence check cannot see it,
// because the node does hold a chunk - just the wrong bytes.
func TestScrubDeepDetectsCorruptionThatFastPassMisses(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)

	chunkID := putScrubObject(t, lilio, "rot", []byte("bits will rot here"))
	victim := mockBackend(t, lilio, "mock-backend-2")
	victim.StoreChunk(context.Background(), chunkID, []byte("corrupted"))

	fast, err := lilio.Scrub(context.Background(), ScrubOptions{})
	if err != nil {
		t.Fatalf("Fast scrub failed: %v", err)
	}
	if !fast.Healthy() {
		t.Error("A presence-only pass should not have noticed corruption")
	}

	deep, err := lilio.Scrub(context.Background(), ScrubOptions{Deep: true})
	if err != nil {
		t.Fatalf("Deep scrub failed: %v", err)
	}
	if deep.ReplicasRestored != 1 {
		t.Errorf("Expected the deep pass to restore 1 replica, got %d", deep.ReplicasRestored)
	}
	restored, err := victim.RetrieveChunk(context.Background(), chunkID)
	if err != nil {
		t.Fatalf("Could not read the repaired replica: %v", err)
	}
	if CalculateChecksum(restored) == CalculateChecksum([]byte("corrupted")) {
		t.Error("Deep scrub did not overwrite the corrupted replica")
	}
}

// When every copy is gone the chunk is lost. Saying so loudly is the point -
// silent loss is exactly what a scrubber exists to prevent.
func TestScrubReportsUnrepairableChunk(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)

	chunkID := putScrubObject(t, lilio, "lost", []byte("this will be destroyed"))
	for _, name := range []string{"mock-backend-0", "mock-backend-1", "mock-backend-2"} {
		mockBackend(t, lilio, name).DeleteChunk(context.Background(), chunkID)
	}

	report, err := lilio.Scrub(context.Background(), ScrubOptions{})
	if err != nil {
		t.Fatalf("Scrub failed: %v", err)
	}
	if report.ChunksUnrepairable != 1 {
		t.Errorf("Expected 1 unrepairable chunk, got %d", report.ChunksUnrepairable)
	}
	if report.Healthy() {
		t.Error("A lost chunk must not be reported as healthy")
	}
	if len(report.Issues) != 1 || !report.Issues[0].Unrepairable {
		t.Errorf("Expected the issue to be marked unrepairable, got %+v", report.Issues)
	}
}

// A dry run reports without touching anything.
func TestScrubDryRunRepairsNothing(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)

	chunkID := putScrubObject(t, lilio, "untouched", []byte("dry run only"))
	victim := mockBackend(t, lilio, "mock-backend-1")
	victim.DeleteChunk(context.Background(), chunkID)

	report, err := lilio.Scrub(context.Background(), ScrubOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Scrub failed: %v", err)
	}
	if victim.HasChunk(context.Background(), chunkID) {
		t.Error("Dry run wrote data")
	}
	if len(report.Issues) != 1 {
		t.Errorf("Dry run should still report the issue, got %d", len(report.Issues))
	}
	if report.ReplicasRestored != 0 {
		t.Errorf("Dry run restored %d replicas", report.ReplicasRestored)
	}
}

// Chunks no metadata refers to are reported but never deleted: a PUT writes its
// chunks before committing metadata, so a scrub in that window would see a live
// upload as garbage.
func TestScrubReportsOrphansWithoutDeleting(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)
	putScrubObject(t, lilio, "real", []byte("a real object"))

	stray := mockBackend(t, lilio, "mock-backend-0")
	stray.StoreChunk(context.Background(), "left-over-from-a-crashed-write", []byte("orphan"))

	report, err := lilio.Scrub(context.Background(), ScrubOptions{})
	if err != nil {
		t.Fatalf("Scrub failed: %v", err)
	}

	found := report.OrphanChunks["mock-backend-0"]
	if len(found) != 1 || found[0] != "left-over-from-a-crashed-write" {
		t.Errorf("Expected the orphan to be reported, got %v", report.OrphanChunks)
	}
	if !stray.HasChunk(context.Background(), "left-over-from-a-crashed-write") {
		t.Error("Scrub deleted an orphan; it must only report them")
	}
}

// A scrub of a large store must stop when its context is cancelled.
func TestScrubHonoursContextCancellation(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2)
	defer cleanup(lilio)
	addMockBackends(lilio, 3)
	for _, key := range []string{"a", "b", "c"} {
		putScrubObject(t, lilio, key, []byte("data"))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := lilio.Scrub(ctx, ScrubOptions{}); err == nil {
		t.Error("Expected a cancelled scrub to return an error")
	}
}
