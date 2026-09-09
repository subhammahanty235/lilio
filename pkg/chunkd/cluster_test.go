package chunkd_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/subhammahanty235/lilio/pkg/chunkd"
	"github.com/subhammahanty235/lilio/pkg/metadata"
	"github.com/subhammahanty235/lilio/pkg/metrics"
	"github.com/subhammahanty235/lilio/pkg/storage"
	storagemodels "github.com/subhammahanty235/lilio/pkg/storage/storage-models"
)

// cluster is a coordinator plus real chunk servers, each reached over a real
// socket. Nothing here is mocked: the coordinator talks HTTP to chunkd exactly
// as it would to a daemon on another machine.
type cluster struct {
	lilio   *storage.Lilio
	servers []*httptest.Server
	toggles []*toggleHandler
}

// toggleHandler lets a test take a node out of service and put it back, which
// httptest.Server alone cannot do - Close is permanent.
type toggleHandler struct {
	inner http.Handler
	mu    sync.RWMutex
	down  bool
}

func (t *toggleHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mu.RLock()
	down := t.down
	t.mu.RUnlock()

	if down {
		http.Error(w, "node down", http.StatusServiceUnavailable)
		return
	}
	t.inner.ServeHTTP(w, r)
}

func (t *toggleHandler) setDown(down bool) {
	t.mu.Lock()
	t.down = down
	t.mu.Unlock()
}

func newCluster(t *testing.T, nodes int) *cluster {
	t.Helper()

	root := t.TempDir()
	lilio, err := storage.NewLilioInstance(storage.Config{
		BasePath:          root,
		ChunkSize:         1024,
		ReplicationFactor: nodes,
		Quorum:            &storage.QuorumConfig{N: nodes, W: 2},
		MetadataConfig:    &metadata.Config{Type: metadata.StoreTypeMemory},
		MetricsConfig:     &metrics.Config{Enabled: false},
	})
	if err != nil {
		t.Fatalf("Failed to create coordinator: %v", err)
	}
	if err := lilio.CreateBucket("data"); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	c := &cluster{lilio: lilio}
	for i := 0; i < nodes; i++ {
		name := fmt.Sprintf("node-%d", i)

		disk, err := storagemodels.NewLocalBackendPod(name, filepath.Join(root, name), 0)
		if err != nil {
			t.Fatalf("Failed to create chunkd storage: %v", err)
		}
		toggle := &toggleHandler{inner: chunkd.New(disk, name, 0).Routes()}
		srv := httptest.NewServer(toggle)
		c.servers = append(c.servers, srv)
		c.toggles = append(c.toggles, toggle)

		backend, err := storagemodels.NewRemoteBackend(name, srv.URL, i, 0)
		if err != nil {
			t.Fatalf("Failed to create remote backend: %v", err)
		}
		if err := lilio.AddBackend(backend); err != nil {
			t.Fatalf("Failed to add backend: %v", err)
		}
	}

	t.Cleanup(func() {
		for _, srv := range c.servers {
			srv.Close()
		}
	})
	return c
}

// kill takes a chunk server down without removing it from the ring, which is
// what an unreachable machine actually looks like to the coordinator.
func (c *cluster) kill(i int) { c.servers[i].Close() }

// stop and start take a node out of service without dropping its data, the way
// a machine that reboots does.
func (c *cluster) stop(i int)  { c.toggles[i].setDown(true) }
func (c *cluster) start(i int) { c.toggles[i].setDown(false) }

// End-to-end: an object written through the coordinator is stored on three
// separate chunk servers over HTTP and reads back byte for byte.
func TestClusterRoundTrip(t *testing.T) {
	c := newCluster(t, 3)
	ctx := context.Background()

	payload := bytes.Repeat([]byte("distributed storage "), 500) // several chunks
	if _, err := c.lilio.PutObject(ctx, "data", "obj", bytes.NewReader(payload), int64(len(payload)), "text/plain"); err != nil {
		t.Fatalf("Put over the network failed: %v", err)
	}

	var buf bytes.Buffer
	if err := c.lilio.GetObject(ctx, "data", "obj", &buf); err != nil {
		t.Fatalf("Get over the network failed: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Errorf("Round trip corrupted the object: got %d bytes, want %d", buf.Len(), len(payload))
	}
}

// Failure test: losing one of three nodes must not lose the object. This is the
// scenario replication exists for, and it is the first time in Lilio's history
// that it can actually be tested - a local directory cannot become unreachable.
func TestClusterSurvivesNodeLoss(t *testing.T) {
	c := newCluster(t, 3)
	ctx := context.Background()

	payload := bytes.Repeat([]byte("survive me "), 400)
	if _, err := c.lilio.PutObject(ctx, "data", "obj", bytes.NewReader(payload), int64(len(payload)), "text/plain"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	c.kill(1)

	var buf bytes.Buffer
	if err := c.lilio.GetObject(ctx, "data", "obj", &buf); err != nil {
		t.Fatalf("Read should survive one node loss with R=2: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Error("Object was corrupted by the node loss")
	}
}

// Failure test: a write must still succeed while one node is unreachable,
// because W=2 of N=3 is reachable.
func TestClusterWriteSucceedsWithNodeDown(t *testing.T) {
	c := newCluster(t, 3)
	ctx := context.Background()

	c.kill(2)

	payload := []byte("written during an outage")
	meta, err := c.lilio.PutObject(ctx, "data", "obj", bytes.NewReader(payload), int64(len(payload)), "text/plain")
	if err != nil {
		t.Fatalf("Write should succeed with 2 of 3 nodes: %v", err)
	}

	// Metadata records where the chunk *belongs*, which is still all three
	// nodes - the dead one included. That is what makes the shortfall visible
	// to a scrub later, rather than looking like a complete write.
	if got := len(meta.Chunks[0].StorageNodes); got != 3 {
		t.Errorf("Expected the intended replica set to be 3 nodes, metadata records %d", got)
	}

	var buf bytes.Buffer
	if err := c.lilio.GetObject(ctx, "data", "obj", &buf); err != nil {
		t.Fatalf("Read back failed: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Error("Object written during the outage came back wrong")
	}
}

// With most of the cluster gone, a read must still succeed from whatever
// intact copy remains. Under the old read quorum this returned an error, even
// though the surviving node held bytes matching the metadata checksum.
func TestClusterReadSurvivesLosingAllButOneNode(t *testing.T) {
	c := newCluster(t, 3)
	ctx := context.Background()

	payload := []byte("only one replica will remain")
	if _, err := c.lilio.PutObject(ctx, "data", "obj", bytes.NewReader(payload), int64(len(payload)), "text/plain"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	c.kill(1)
	c.kill(2)

	var buf bytes.Buffer
	if err := c.lilio.GetObject(ctx, "data", "obj", &buf); err != nil {
		t.Fatalf("Read should succeed from the last intact replica: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Error("The surviving replica returned the wrong data")
	}
}

// A read may only fail once no node can produce the chunk at all.
func TestClusterReadFailsWhenEveryNodeIsGone(t *testing.T) {
	c := newCluster(t, 3)
	ctx := context.Background()

	payload := []byte("nothing will be left")
	if _, err := c.lilio.PutObject(ctx, "data", "obj", bytes.NewReader(payload), int64(len(payload)), "text/plain"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	c.kill(0)
	c.kill(1)
	c.kill(2)

	var buf bytes.Buffer
	if err := c.lilio.GetObject(ctx, "data", "obj", &buf); err == nil {
		t.Fatal("Expected the read to fail with every node unreachable")
	}
}

// Failure test: deleting through the coordinator must reclaim the chunks on
// every chunk server that holds them.
func TestClusterDeleteReclaimsRemoteChunks(t *testing.T) {
	c := newCluster(t, 3)
	ctx := context.Background()

	payload := []byte("delete me from every node")
	meta, err := c.lilio.PutObject(ctx, "data", "obj", bytes.NewReader(payload), int64(len(payload)), "text/plain")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	if err := c.lilio.DeleteObject(ctx, "data", "obj"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	for _, name := range meta.Chunks[0].StorageNodes {
		backend, err := c.lilio.Registry.Get(name)
		if err != nil {
			t.Fatalf("Backend %s missing: %v", name, err)
		}
		if backend.HasChunk(ctx, meta.Chunks[0].ChunkID) {
			t.Errorf("Chunk still present on %s after delete", name)
		}
	}
}

// The scenario the scrubber exists for, end to end over real sockets: a node is
// down when a write lands, comes back later, and nothing about reading the
// object would ever restore its copy.
func TestClusterScrubRestoresReplicaAfterNodeReturns(t *testing.T) {
	c := newCluster(t, 3)
	ctx := context.Background()

	c.stop(2)

	payload := []byte("written while node-2 was down")
	meta, err := c.lilio.PutObject(ctx, "data", "obj", bytes.NewReader(payload), int64(len(payload)), "text/plain")
	if err != nil {
		t.Fatalf("Write should succeed with 2 of 3 nodes: %v", err)
	}
	chunkID := meta.Chunks[0].ChunkID

	c.start(2)

	recovered, err := c.lilio.Registry.Get("node-2")
	if err != nil {
		t.Fatalf("node-2 missing from registry: %v", err)
	}

	// Reading the object does not help: node-2 answers "no such chunk", and a
	// replica that errors is skipped by read repair rather than restored.
	var buf bytes.Buffer
	if err := c.lilio.GetObject(ctx, "data", "obj", &buf); err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if recovered.HasChunk(ctx, chunkID) {
		t.Fatal("Test premise wrong: a read restored the replica")
	}

	report, err := c.lilio.Scrub(ctx, storage.ScrubOptions{})
	if err != nil {
		t.Fatalf("Scrub failed: %v", err)
	}

	if !recovered.HasChunk(ctx, chunkID) {
		t.Error("Scrub did not restore the replica on the returned node")
	}
	if report.ReplicasRestored != 1 {
		t.Errorf("Expected 1 replica restored, got %d (%s)", report.ReplicasRestored, report)
	}

	// And a second pass over a now-complete cluster must find nothing.
	again, err := c.lilio.Scrub(ctx, storage.ScrubOptions{Deep: true})
	if err != nil {
		t.Fatalf("Second scrub failed: %v", err)
	}
	if !again.Healthy() {
		t.Errorf("Expected a clean second pass, got %+v", again.Issues)
	}
}
