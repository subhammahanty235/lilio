package storagemodels

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/subhammahanty235/lilio/internal/fsatomic"
)

// Failure test: a chunk write interrupted by a crash must leave the previous
// chunk intact, never a truncated file.
//
// This is the case that matters, and it is not the concurrent-reader case -
// LocalBackendPod holds a mutex across a write, so readers inside this process
// are already serialised against it whatever the write does. A kill has no such
// protection: os.WriteFile truncates the destination and then fills it, so a
// process that dies in between leaves a short file that persists. Worse, that
// file still exists and still answers yes to HasChunk, so the cheap scrub mode
// would count the replica as healthy and the damage would sit there until a
// deep scrub happened to look.
//
// A child process is used because the failure needs a real kill; the parent
// then inspects what was left behind.
func TestChunkSurvivesCrashDuringWrite(t *testing.T) {
	if os.Getenv("LILIO_CRASH_HELPER") == "1" {
		crashHelperWriteForever()
		return
	}

	dir := t.TempDir()
	ctx := context.Background()

	backend, err := NewLocalBackendPod("node", dir, 0)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}

	original := bytes.Repeat([]byte("g"), 1<<20)
	if err := backend.StoreChunk(ctx, crashChunkID, original); err != nil {
		t.Fatalf("Setup write failed: %v", err)
	}

	killed := 0
	for attempt := 0; attempt < 8; attempt++ {
		cmd := exec.Command(os.Args[0], "-test.run=TestChunkSurvivesCrashDuringWrite")
		cmd.Env = append(os.Environ(), "LILIO_CRASH_HELPER=1", "LILIO_CRASH_DIR="+dir)
		if err := cmd.Start(); err != nil {
			t.Fatalf("Failed to start the helper: %v", err)
		}

		time.Sleep(40 * time.Millisecond) // long enough to be inside a write
		cmd.Process.Kill()
		cmd.Wait()
		killed++

		data, err := os.ReadFile(filepath.Join(dir, crashChunkID))
		if err != nil {
			t.Fatalf("The chunk disappeared entirely after a crash: %v", err)
		}
		if len(data) != len(original) && len(data) != crashPayloadSize {
			t.Fatalf("Crash left a truncated chunk: %d bytes, which is neither the "+
				"original %d nor a complete new write of %d",
				len(data), len(original), crashPayloadSize)
		}
	}

	if killed == 0 {
		t.Fatal("No helper was actually killed; the test proved nothing")
	}

	// Abandoned temp files are expected. They must not be mistaken for chunks.
	chunks, err := backend.ListChunks(ctx)
	if err != nil {
		t.Fatalf("ListChunks failed: %v", err)
	}
	for _, name := range chunks {
		if name != crashChunkID {
			t.Errorf("ListChunks reported %q, which is not a chunk", name)
		}
	}
}

const (
	crashChunkID     = "crash-target"
	crashPayloadSize = 48 << 20 // big enough that a write is usually in progress
)

// crashHelperWriteForever writes the same chunk over and over until killed.
func crashHelperWriteForever() {
	dir := os.Getenv("LILIO_CRASH_DIR")
	backend, err := NewLocalBackendPod("helper", dir, 0)
	if err != nil {
		os.Exit(1)
	}

	payload := bytes.Repeat([]byte("n"), crashPayloadSize)
	for {
		backend.StoreChunk(context.Background(), crashChunkID, payload)
	}
}

// An interrupted write leaves its temp file behind. It is not a chunk, and
// listing it as one would have the scrubber report it as an orphan.
func TestListChunksIgnoresInterruptedWrites(t *testing.T) {
	dir := t.TempDir()
	backend, err := NewLocalBackendPod("node", dir, 0)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	ctx := context.Background()

	if err := backend.StoreChunk(ctx, "real-chunk", []byte("data")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Simulate a write that died before its rename.
	leftover := filepath.Join(dir, fsatomic.TempPrefix+"abandoned")
	if err := os.WriteFile(leftover, []byte("half a chunk"), 0644); err != nil {
		t.Fatalf("Failed to plant a temp file: %v", err)
	}

	chunks, err := backend.ListChunks(ctx)
	if err != nil {
		t.Fatalf("ListChunks failed: %v", err)
	}
	if len(chunks) != 1 || chunks[0] != "real-chunk" {
		t.Errorf("ListChunks returned %v, want [real-chunk]", chunks)
	}
}

// Presence must imply completeness: a stored chunk always reads back whole.
func TestStoredChunkIsCompleteWhenPresent(t *testing.T) {
	backend, err := NewLocalBackendPod("node", t.TempDir(), 0)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	ctx := context.Background()

	payload := bytes.Repeat([]byte("chunk contents "), 10000)
	if err := backend.StoreChunk(ctx, "c1", payload); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if !backend.HasChunk(ctx, "c1") {
		t.Fatal("HasChunk says no for a chunk just written")
	}
	got, err := backend.RetrieveChunk(ctx, "c1")
	if err != nil {
		t.Fatalf("RetrieveChunk failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("Got %d bytes, want %d", len(got), len(payload))
	}
}

// BenchmarkStoreChunk measures the write path at Lilio's default chunk size.
// Atomic writes add a temp file, two fsyncs and a rename; this is what that
// costs per chunk.
func BenchmarkStoreChunk(b *testing.B) {
	backend, err := NewLocalBackendPod("bench", b.TempDir(), 0)
	if err != nil {
		b.Fatalf("Failed to create backend: %v", err)
	}
	ctx := context.Background()
	payload := bytes.Repeat([]byte("x"), 1<<20)

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := backend.StoreChunk(ctx, "bench-chunk", payload); err != nil {
			b.Fatalf("StoreChunk failed: %v", err)
		}
	}
}
