package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subhammahanty235/lilio/pkg/metadata"
	"github.com/subhammahanty235/lilio/pkg/metrics"
	"github.com/subhammahanty235/lilio/pkg/storage"
	storagemodels "github.com/subhammahanty235/lilio/pkg/storage/storage-models"
)

// testServer brings up a Lilio instance backed by three local directories and
// serves it over httptest, returning the base URL and the backend paths so a
// test can damage storage directly.
func testServer(t *testing.T) (string, []string) {
	t.Helper()

	root := t.TempDir()
	lio, err := storage.NewLilioInstance(storage.Config{
		BasePath:          root,
		ChunkSize:         16, // small, so modest payloads still span many chunks
		ReplicationFactor: 3,
		Quorum:            &storage.QuorumConfig{N: 3, W: 2},
		MetadataConfig:    &metadata.Config{Type: metadata.StoreTypeMemory},
		MetricsConfig:     &metrics.Config{Enabled: false},
	})
	if err != nil {
		t.Fatalf("Failed to create Lilio instance: %v", err)
	}

	var paths []string
	for i := 0; i < 3; i++ {
		path := filepath.Join(root, fmt.Sprintf("backend-%d", i))
		backend, err := storagemodels.NewLocalBackendPod(fmt.Sprintf("backend-%d", i), path, i)
		if err != nil {
			t.Fatalf("Failed to create backend: %v", err)
		}
		if err := lio.AddBackend(backend); err != nil {
			t.Fatalf("Failed to add backend: %v", err)
		}
		paths = append(paths, path)
	}

	srv := httptest.NewServer(NewServer(lio, "test").Handler())
	t.Cleanup(srv.Close)

	resp, err := http.DefaultClient.Do(mustRequest(t, http.MethodPut, srv.URL+"/b", ""))
	if err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}
	resp.Body.Close()

	return srv.URL, paths
}

func mustRequest(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("Failed to build request: %v", err)
	}
	return req
}

func do(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(mustRequest(t, method, url, body))
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// removeChunk deletes one chunk from every backend that might hold it,
// simulating storage that has lost the data entirely.
func removeChunk(t *testing.T, paths []string, chunkID string) {
	t.Helper()
	for _, path := range paths {
		if err := os.Remove(filepath.Join(path, chunkID)); err != nil && !os.IsNotExist(err) {
			t.Fatalf("Failed to remove chunk: %v", err)
		}
	}
}

// Integration test: DELETE must actually delete, and say so with 204.
// Previously an unhandled method fell through the switch and net/http answered
// 200 with an empty body, so the CLI reported success for a no-op.
func TestDeleteObjectEndpoint(t *testing.T) {
	baseURL, _ := testServer(t)

	resp := do(t, http.MethodPut, baseURL+"/b/gone.txt", "delete me")
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT: got %d, want 201", resp.StatusCode)
	}

	resp = do(t, http.MethodDelete, baseURL+"/b/gone.txt", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE: got %d, want 204", resp.StatusCode)
	}

	resp = do(t, http.MethodGet, baseURL+"/b/gone.txt", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET after DELETE: got %d, want 404", resp.StatusCode)
	}

	// Idempotent: deleting again is still a success.
	resp = do(t, http.MethodDelete, baseURL+"/b/gone.txt", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("Second DELETE: got %d, want 204", resp.StatusCode)
	}
}

// Integration test: HEAD returns GET's headers and no body.
func TestHeadObjectEndpoint(t *testing.T) {
	baseURL, _ := testServer(t)

	const body = "head me please"
	resp := do(t, http.MethodPut, baseURL+"/b/head.txt", body)
	resp.Body.Close()

	resp = do(t, http.MethodHead, baseURL+"/b/head.txt", "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD: got %d, want 200", resp.StatusCode)
	}
	if got := resp.ContentLength; got != int64(len(body)) {
		t.Errorf("HEAD Content-Length: got %d, want %d", got, len(body))
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("HEAD: expected an ETag")
	}
	if data, _ := io.ReadAll(resp.Body); len(data) != 0 {
		t.Errorf("HEAD returned a body of %d bytes", len(data))
	}

	resp = do(t, http.MethodHead, baseURL+"/b/absent.txt", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("HEAD on a missing object: got %d, want 404", resp.StatusCode)
	}
}

// Integration test: an unsupported method must be refused, not silently
// answered with 200.
func TestUnsupportedMethodReturns405(t *testing.T) {
	baseURL, _ := testServer(t)

	resp := do(t, http.MethodPatch, baseURL+"/b/anything.txt", "x")
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("PATCH: got %d, want 405", resp.StatusCode)
	}
}

// Failure test: when an object's data cannot be recovered at all, the response
// must not claim success. This is the case that previously produced
// "200 OK, Content-Length: 13" with an empty body.
func TestUnreadableObjectDoesNotReturn200(t *testing.T) {
	baseURL, paths := testServer(t)

	const body = "this content will be destroyed"
	resp := do(t, http.MethodPut, baseURL+"/b/lost.txt", body)
	resp.Body.Close()

	// Destroy the first chunk on every backend.
	entries, err := os.ReadDir(paths[0])
	if err != nil {
		t.Fatalf("Failed to read backend dir: %v", err)
	}
	var first string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_chunk_0") {
			first = e.Name()
		}
	}
	if first == "" {
		t.Fatal("Could not find chunk 0 on disk")
	}
	removeChunk(t, paths, first)

	resp = do(t, http.MethodGet, baseURL+"/b/lost.txt", "")
	defer resp.Body.Close()
	data, readErr := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK && readErr == nil && len(data) < len(body) {
		t.Errorf("Unrecoverable read reported success: status %d, %d of %d bytes, no error",
			resp.StatusCode, len(data), len(body))
	}
	if resp.StatusCode == http.StatusOK {
		t.Errorf("Expected a non-200 status when nothing could be read, got %d", resp.StatusCode)
	}
}

// Failure test: when a read fails partway, after the status line is already on
// the wire, the client must see a broken transfer rather than a short body that
// looks complete.
func TestTruncatedReadIsNotSilent(t *testing.T) {
	baseURL, paths := testServer(t)

	// 16-byte chunks, so this spans several and chunk 0 stays intact.
	body := strings.Repeat("abcdefghij", 20)
	resp := do(t, http.MethodPut, baseURL+"/b/partial.txt", body)
	resp.Body.Close()

	entries, err := os.ReadDir(paths[0])
	if err != nil {
		t.Fatalf("Failed to read backend dir: %v", err)
	}
	var later string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_chunk_3") {
			later = e.Name()
		}
	}
	if later == "" {
		t.Fatal("Could not find a later chunk on disk")
	}
	removeChunk(t, paths, later)

	// The failure may surface either as a transport error (the abort lands
	// before net/http flushes its response buffer) or as a read error partway
	// through the body. Both are acceptable; a clean short read is not.
	getResp, getErr := http.DefaultClient.Do(mustRequest(t, http.MethodGet, baseURL+"/b/partial.txt", ""))
	if getErr != nil {
		t.Logf("client saw a transport error, as intended: %v", getErr)
		return
	}
	defer getResp.Body.Close()

	data, readErr := io.ReadAll(getResp.Body)
	if readErr != nil {
		t.Logf("client saw a read error, as intended: %v", readErr)
		return
	}
	if len(data) == len(body) {
		t.Fatal("Expected the read to fail; it returned the whole object")
	}
	t.Errorf("Truncated read returned %d of %d bytes with status %d and no error - "+
		"a client would treat this as a complete object",
		len(data), len(body), getResp.StatusCode)
}
