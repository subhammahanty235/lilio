package chunkd_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
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
}

func newCluster(t *testing.T, nodes int) *cluster {
	t.Helper()

	root := t.TempDir()
	lilio, err := storage.NewLilioInstance(storage.Config{
		BasePath:          root,
		ChunkSize:         1024,
		ReplicationFactor: nodes,
		Quorum:            &storage.QuorumConfig{N: nodes, W: 2, R: 2},
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
		srv := httptest.NewServer(chunkd.New(disk, name, 0).Routes())
		c.servers = append(c.servers, srv)

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

	// The surviving replicas are recorded; the dead node is not. That the
	// object is now under-replicated, and that nothing yet notices or repairs
	// it, is a known gap - see the repair milestone.
	if got := len(meta.Chunks[0].StorageNodes); got != 2 {
		t.Errorf("Expected the chunk on 2 nodes, metadata records %d", got)
	}

	var buf bytes.Buffer
	if err := c.lilio.GetObject(ctx, "data", "obj", &buf); err != nil {
		t.Fatalf("Read back failed: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Error("Object written during the outage came back wrong")
	}
}

// Failure test: with too few replicas reachable the read must fail, not return
// a partial or empty object.
func TestClusterReadFailsBelowQuorum(t *testing.T) {
	c := newCluster(t, 3)
	ctx := context.Background()

	payload := []byte("only one replica will remain")
	if _, err := c.lilio.PutObject(ctx, "data", "obj", bytes.NewReader(payload), int64(len(payload)), "text/plain"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	c.kill(1)
	c.kill(2)

	var buf bytes.Buffer
	if err := c.lilio.GetObject(ctx, "data", "obj", &buf); err == nil {
		t.Fatal("Expected the read to fail with only 1 of 3 nodes and R=2")
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
