package storagemodels

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Unit test: the happy path against a stand-in chunk server.
func TestRemoteBackendRoundTrip(t *testing.T) {
	stored := make(map[string][]byte)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut:
			buf := new(bytes.Buffer)
			buf.ReadFrom(r.Body)
			stored["c1"] = buf.Bytes()
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/chunks/c1":
			w.Write(stored["c1"])
		case r.Method == http.MethodHead:
			if _, ok := stored["c1"]; !ok {
				w.WriteHeader(http.StatusNotFound)
			}
		case r.Method == http.MethodDelete:
			delete(stored, "c1")
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/chunks":
			w.Write([]byte(`{"chunks":["c1"]}`))
		case r.URL.Path == "/stats":
			w.Write([]byte(`{"bytes_used":42,"chunks_stored":1}`))
		case r.URL.Path == "/health":
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	backend, err := NewRemoteBackend("node-1", srv.URL, 1, time.Second)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	ctx := context.Background()
	payload := []byte("chunk contents")

	if err := backend.StoreChunk(ctx, "c1", payload); err != nil {
		t.Fatalf("StoreChunk: %v", err)
	}
	got, err := backend.RetrieveChunk(ctx, "c1")
	if err != nil {
		t.Fatalf("RetrieveChunk: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("RetrieveChunk returned %q, want %q", got, payload)
	}
	if !backend.HasChunk(ctx, "c1") {
		t.Error("HasChunk: got false for a stored chunk")
	}
	if chunks, err := backend.ListChunks(ctx); err != nil || len(chunks) != 1 {
		t.Errorf("ListChunks: got %v, %v", chunks, err)
	}
	if stats, err := backend.Stats(ctx); err != nil || stats.BytesUsed != 42 {
		t.Errorf("Stats: got %+v, %v", stats, err)
	}
	if err := backend.Health(ctx); err != nil {
		t.Errorf("Health: %v", err)
	}
	if err := backend.DeleteChunk(ctx, "c1"); err != nil {
		t.Errorf("DeleteChunk: %v", err)
	}
	if backend.HasChunk(ctx, "c1") {
		t.Error("HasChunk: got true after delete")
	}
}

// Failure test: a chunk the node does not have must read as an error, not as
// empty data - otherwise a missing replica would look like a valid empty chunk.
func TestRemoteBackendMissingChunk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	backend, _ := NewRemoteBackend("node-1", srv.URL, 1, time.Second)
	if _, err := backend.RetrieveChunk(context.Background(), "absent"); err == nil {
		t.Error("Expected an error for a chunk the node does not have")
	}

	// Deleting something already gone is still a success: a retry after a
	// timeout cannot be distinguished from a first attempt.
	if err := backend.DeleteChunk(context.Background(), "absent"); err != nil {
		t.Errorf("DeleteChunk on a missing chunk should succeed, got: %v", err)
	}
}

// Failure test: a node that is not listening at all must fail promptly.
func TestRemoteBackendUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	backend, _ := NewRemoteBackend("node-1", url, 1, time.Second)
	if err := backend.StoreChunk(context.Background(), "c1", []byte("x")); err == nil {
		t.Error("Expected an error when the node is not listening")
	}

	// Info reports the failure without doing any I/O of its own.
	if status := backend.Info().Status; status != "offline" {
		t.Errorf("Info().Status after a failed call: got %q, want offline", status)
	}
}

// Failure test: this is the case a local directory can never produce - a node
// that accepts the connection and then never answers. Without a deadline the
// call would block forever, and since writes wait for every replica, it would
// block the whole request behind it.
func TestRemoteBackendTimesOutOnSilentNode(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // accept, then say nothing
	}))
	// Ordering matters: Close waits for in-flight handlers, so the handler has
	// to be released first. Defers run last-registered-first.
	defer srv.Close()
	defer close(release)

	backend, _ := NewRemoteBackend("silent", srv.URL, 1, 200*time.Millisecond)

	start := time.Now()
	err := backend.StoreChunk(context.Background(), "c1", []byte("x"))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Expected a timeout error from a silent node")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Call took %v; the backend timeout should have bounded it", elapsed)
	}
}

// Failure test: a caller that gives up must be able to abandon the call - this
// is what lets a disconnecting client stop the work it started.
func TestRemoteBackendHonoursCallerCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	// A long backend timeout, so only the caller's cancellation can end this.
	backend, _ := NewRemoteBackend("slow", srv.URL, 1, 30*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if _, err := backend.RetrieveChunk(ctx, "c1"); err == nil {
		t.Fatal("Expected an error after the caller cancelled")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Call took %v; cancelling the context should have ended it", elapsed)
	}
}

// Unit test: a bad URL is rejected when the backend is built, not on first use.
func TestRemoteBackendRejectsBadURL(t *testing.T) {
	for _, url := range []string{"", "not-a-url", "ftp://host:9000", "http://"} {
		if _, err := NewRemoteBackend("n", url, 1, time.Second); err == nil {
			t.Errorf("Expected %q to be rejected", url)
		}
	}
}
