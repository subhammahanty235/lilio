package storage

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/subhammahanty235/lilio/pkg/metadata"
)

// Concurrency test: two writers racing for the same key.
//
// Before metadata had compare-and-swap, both writes committed. The later one
// won, the earlier one's chunks were left referenced by nothing, and neither
// client was told anything had happened - the losing writer got a 201 for an
// object that no longer existed by the time it was told so.
//
// Now exactly one commits. The loser is told it lost and removes the chunks it
// wrote, so nothing is orphaned.
func TestConcurrentWritesToSameKey(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2)
	defer cleanup(lilio)

	// Slow writes hold both uploads open long enough that both read the same
	// revision before either commits, which is the situation being tested.
	for i := 0; i < 3; i++ {
		lilio.AddBackend(&MockBackend{
			name:       "mock-backend-" + string(rune('0'+i)),
			chunks:     make(map[string][]byte),
			storeDelay: 40 * time.Millisecond,
		})
	}

	payloads := map[string][]byte{
		"writer-a": bytes.Repeat([]byte("a"), 600),
		"writer-b": bytes.Repeat([]byte("b"), 900),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := map[string]error{}
	metas := map[string]*metadata.ObjectMetadata{}

	for name, payload := range payloads {
		wg.Add(1)
		go func(name string, payload []byte) {
			defer wg.Done()
			meta, err := lilio.PutObject(context.Background(), "test-bucket", "contested",
				bytes.NewReader(payload), int64(len(payload)), "text/plain")
			mu.Lock()
			results[name] = err
			metas[name] = meta
			mu.Unlock()
		}(name, payload)
	}
	wg.Wait()

	var winners, losers []string
	for name, err := range results {
		switch {
		case err == nil:
			winners = append(winners, name)
		case errors.Is(err, metadata.ErrRevisionMismatch):
			losers = append(losers, name)
		default:
			t.Fatalf("%s failed for an unexpected reason: %v", name, err)
		}
	}

	if len(winners) != 1 {
		t.Fatalf("Expected exactly one write to commit, got %d (%v)", len(winners), winners)
	}
	if len(losers) != 1 {
		t.Fatalf("Expected exactly one write to be rejected, got %d", len(losers))
	}

	// The object must be the winner's, whole.
	winner := winners[0]
	var buf bytes.Buffer
	if err := lilio.GetObject(context.Background(), "test-bucket", "contested", &buf); err != nil {
		t.Fatalf("Read after the race failed: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), payloads[winner]) {
		t.Errorf("Object is %d bytes; the winner (%s) wrote %d", buf.Len(), winner, len(payloads[winner]))
	}

	// And the loser must have left nothing behind.
	time.Sleep(200 * time.Millisecond) // chunk cleanup is detached from the request

	held := chunkIDsOnBackends(t, lilio)
	expected := map[string]bool{}
	for _, chunk := range metas[winner].Chunks {
		expected[chunk.ChunkID] = true
	}
	for id := range held {
		if !expected[id] {
			t.Errorf("Chunk %s is on a backend but belongs to no object - the losing write leaked it", id)
		}
	}
}

// A write to a key that does not exist yet must still be conditional, so two
// simultaneous creates cannot both believe they succeeded.
func TestConcurrentCreatesOfSameKey(t *testing.T) {
	lilio := setupTestLilio(t, 3, 2)
	defer cleanup(lilio)
	for i := 0; i < 3; i++ {
		lilio.AddBackend(&MockBackend{
			name:       "mock-backend-" + string(rune('0'+i)),
			chunks:     make(map[string][]byte),
			storeDelay: 30 * time.Millisecond,
		})
	}

	const writers = 5
	var wg sync.WaitGroup
	var mu sync.Mutex
	committed := 0

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := bytes.Repeat([]byte{byte('0' + i)}, 400)
			_, err := lilio.PutObject(context.Background(), "test-bucket", "fresh",
				bytes.NewReader(payload), int64(len(payload)), "text/plain")
			if err == nil {
				mu.Lock()
				committed++
				mu.Unlock()
				return
			}
			if !errors.Is(err, metadata.ErrRevisionMismatch) {
				t.Errorf("Unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if committed != 1 {
		t.Errorf("Expected exactly one of %d simultaneous creates to commit, got %d", writers, committed)
	}
}
