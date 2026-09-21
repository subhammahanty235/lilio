package storagemodels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/subhammahanty235/lilio/pkg/storage"
)

// DefaultRemoteTimeout bounds a single request to a chunk server when the
// caller's context carries no deadline of its own.
//
// The value is a compromise. Too short and a healthy but busy node starts
// failing writes that would have succeeded; too long and one sick node holds
// the caller's request open, degrading everything behind it. 10s is generous
// for a 1 MB chunk on a local network and still short enough that a hung node
// fails fast rather than accumulating stuck requests.
const DefaultRemoteTimeout = 10 * time.Second

// RemoteBackend stores chunks on a lilio-chunkd running on another machine.
//
// It is the piece that turns "a folder on this host" into "a node on the
// network". Everything above it - the hash ring, quorum, read repair - is
// unchanged, because this satisfies the same StorageBackend interface as the
// local and Google Drive backends.
type RemoteBackend struct {
	name     string
	baseURL  string
	priority int
	timeout  time.Duration
	client   *http.Client

	// Info must not perform I/O, so reachability is remembered from the last
	// call that actually talked to the node rather than probed on demand.
	mu      sync.RWMutex
	healthy bool
}

func NewRemoteBackend(name, rawURL string, priority int, timeout time.Duration) (*RemoteBackend, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url %q: %w", rawURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("url must be http or https, got %q", rawURL)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("url must include a host: %q", rawURL)
	}
	if timeout <= 0 {
		timeout = DefaultRemoteTimeout
	}

	return &RemoteBackend{
		name:     name,
		baseURL:  strings.TrimRight(rawURL, "/"),
		priority: priority,
		timeout:  timeout,
		// No Timeout on the client itself: the deadline comes from the context,
		// so a caller with a shorter budget is honoured and one with a longer
		// budget is still capped by withTimeout below.
		client:  &http.Client{},
		healthy: true, // assumed reachable until a call proves otherwise
	}, nil
}

// withTimeout caps the caller's context at this backend's timeout. A context
// that already expires sooner is left alone.
func (r *RemoteBackend) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= r.timeout {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, r.timeout)
}

func (r *RemoteBackend) chunkURL(chunkID string) string {
	return r.baseURL + "/chunks/" + url.PathEscape(chunkID)
}

func (r *RemoteBackend) setHealthy(healthy bool) {
	r.mu.Lock()
	r.healthy = healthy
	r.mu.Unlock()
}

// do issues one request, recording whether the node answered at all. A
// transport error means unreachable; any HTTP response, even an error status,
// means the node is alive.
func (r *RemoteBackend) do(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	ctx, cancel := r.withTimeout(ctx)

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		cancel()
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}

	resp, err := r.client.Do(req)
	if err != nil {
		cancel()
		r.setHealthy(false)
		return nil, fmt.Errorf("%s unreachable: %w", r.name, err)
	}
	r.setHealthy(true)

	// The caller closes the body; cancelling then releases the context.
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnClose releases a request's context once its body is closed, so the
// deadline covers reading the response as well as receiving the headers.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func (r *RemoteBackend) Info() storage.BackendInfo {
	r.mu.RLock()
	healthy := r.healthy
	r.mu.RUnlock()

	status := storage.StatusOnline
	if !healthy {
		status = storage.StatusOffline
	}

	return storage.BackendInfo{
		Name:     r.name,
		Type:     storage.BackendTypeRemote,
		Status:   status,
		Priority: r.priority,
	}
}

func (r *RemoteBackend) Health(ctx context.Context) error {
	resp, err := r.do(ctx, http.MethodGet, r.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s unhealthy: status %d", r.name, resp.StatusCode)
	}
	return nil
}

func (r *RemoteBackend) StoreChunk(ctx context.Context, chunkID string, data []byte) error {
	resp, err := r.do(ctx, http.MethodPut, r.chunkURL(chunkID), data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to store chunk %s on %s: status %d", chunkID, r.name, resp.StatusCode)
	}
	return nil
}

func (r *RemoteBackend) RetrieveChunk(ctx context.Context, chunkID string) ([]byte, error) {
	resp, err := r.do(ctx, http.MethodGet, r.chunkURL(chunkID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("chunk not found: %s", chunkID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to retrieve chunk %s from %s: status %d", chunkID, r.name, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read chunk %s from %s: %w", chunkID, r.name, err)
	}
	return data, nil
}

func (r *RemoteBackend) DeleteChunk(ctx context.Context, chunkID string) error {
	resp, err := r.do(ctx, http.MethodDelete, r.chunkURL(chunkID), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	// A chunk that is already gone is not an error - deletes must be safe to
	// repeat, since a retry after a timeout is indistinguishable from a first
	// attempt.
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("failed to delete chunk %s on %s: status %d", chunkID, r.name, resp.StatusCode)
	}
	return nil
}

func (r *RemoteBackend) HasChunk(ctx context.Context, chunkID string) bool {
	resp, err := r.do(ctx, http.MethodHead, r.chunkURL(chunkID), nil)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

func (r *RemoteBackend) ListChunks(ctx context.Context) ([]string, error) {
	resp, err := r.do(ctx, http.MethodGet, r.baseURL+"/chunks", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list chunks on %s: status %d", r.name, resp.StatusCode)
	}

	var payload struct {
		Chunks []string `json:"chunks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to parse chunk list from %s: %w", r.name, err)
	}
	return payload.Chunks, nil
}

func (r *RemoteBackend) Stats(ctx context.Context) (storage.BackendStats, error) {
	resp, err := r.do(ctx, http.MethodGet, r.baseURL+"/stats", nil)
	if err != nil {
		return storage.BackendStats{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return storage.BackendStats{}, fmt.Errorf("failed to get stats from %s: status %d", r.name, resp.StatusCode)
	}

	var stats storage.BackendStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return storage.BackendStats{}, fmt.Errorf("failed to parse stats from %s: %w", r.name, err)
	}
	stats.LastChecked = time.Now()
	return stats, nil
}

var _ storage.StorageBackend = (*RemoteBackend)(nil)
