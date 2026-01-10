package storage

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/subhammahanty235/lilio/pkg/metadata"
)

type Lilio struct {
	BasePath          string
	ChunkSize         int
	ReplicationFactor int

	StorageNodes map[string]*StorageNode
	Metadata     *metadata.MetadataStore

	mu sync.RWMutex
}

type Config struct {
	BasePath          string
	NumStorageNodes   int
	ChunkSize         int
	ReplicationFactor int
}

func DefaultConig() Config {
	return Config{
		BasePath:          "./lilio_data",
		NumStorageNodes:   4,
		ChunkSize:         1024 * 1024,
		ReplicationFactor: 2,
	}
}

func NewLilioInstance(cfg Config) (*Lilio, error) {
	// metadata store
	metadataStore, err := metadata.NewMetadataStore(filepath.Join(cfg.BasePath, "metadata"))
	if err != nil {
		return nil, fmt.Errorf("Failed to create metadata store: %w", err)
	}

	nodes := make(map[string]*StorageNode)
	for i := 0; i < cfg.NumStorageNodes; i++ {
		nodeId := fmt.Sprintf("node_%d", i)
		nodePath := filepath.Join(cfg.BasePath, "storage_nodes", nodeId)
		node, err := NewStorageNode(nodeId, nodePath)
		if err != nil {
			return nil, fmt.Errorf("failed to create storage node %s: %w", nodeId, err)
		}

		nodes[nodeId] = node
	}

	obj := &Lilio{
		BasePath:          cfg.BasePath,
		ChunkSize:         cfg.ChunkSize,
		ReplicationFactor: cfg.ReplicationFactor,
		StorageNodes:      nodes,
		Metadata:          metadataStore,
	}

	fmt.Printf("MiniS3 initialized:\n")
	fmt.Printf("  - Storage nodes: %d\n", cfg.NumStorageNodes)
	fmt.Printf("  - Chunk size: %d KB\n", cfg.ChunkSize/1024)
	fmt.Printf("  - Replication factor: %d\n", cfg.ReplicationFactor)

	return obj, nil
}

func (s *Lilio) ChunkData(data []byte) [][]byte {
	var chunks [][]byte
	for i := 0; i < len(data); i += s.ChunkSize {
		end := min(i+s.ChunkSize, len(data))
		chunks = append(chunks, data[i:end])
	}

	return chunks
}

func (s *Lilio) SelectNodesForChunk(chunkIndex int) []string {
	// Get sorted node IDs for consistent selection
	var nodeIDs []string
	for id := range s.StorageNodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)

	numNodes := len(nodeIDs)
	startPos := chunkIndex % numNodes

	var selected []string
	for i := 0; i < s.ReplicationFactor && i < numNodes; i++ {
		pos := (startPos + i) % numNodes
		selected = append(selected, nodeIDs[pos])
	}

	return selected
}

// Public API
// Craete bucket
func (s *Lilio) CreateBucket(bucketname string) error {
	return s.Metadata.CreateBucket(bucketname)
}

// List buckets
func (s *Lilio) ListBuckets() ([]string, error) {
	return s.Metadata.ListBuckets()
}

// Todo : Delete buckets

// Put object

func (s *Lilio) PutObject(bucket, key string, data []byte, contentType string) (*metadata.ObjectMetadata, error) {

	// TODO : Cond --> check if bucket exists

	objectId := generateUUID()
	chunks := s.ChunkData(data)
	totalChunks := len(chunks)

	fmt.Printf("\nPutting object: %s/%s\n", bucket, key)
	fmt.Printf("  - Size: %d bytes\n", len(data))
	fmt.Printf("  - Chunks: %d\n", totalChunks)

	var chunkInfos []metadata.ChunkInfo
	for i, chunkData := range chunks {
		chunkId := fmt.Sprintf("%s_chunk_%d", objectId, i)

		chunkCheckSum := CalculateChecksum(chunkData)
		targetNodes := s.SelectNodesForChunk(i)

		var successfulNodes []string
		var wg sync.WaitGroup
		var mu sync.Mutex

		for _, nodeId := range targetNodes {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()

				s.mu.RLock()
				node, exists := s.StorageNodes[id]
				s.mu.RUnlock()

				if !exists {
					return
				}

				if err := node.StoreChunk(chunkId, chunkData); err == nil {
					mu.Lock()
					successfulNodes = append(successfulNodes, id)
					mu.Unlock()
				}
			}(nodeId)
		}

		wg.Wait()
		if len(successfulNodes) == 0 {
			return nil, fmt.Errorf("failed to store chunk %d", i)
		}

		chunkInfo := metadata.ChunkInfo{
			ChunkID:      chunkId,
			ChunkIndex:   i,
			Size:         int64(len(chunkData)),
			Checksum:     chunkCheckSum,
			StorageNodes: successfulNodes,
		}

		chunkInfos = append(chunkInfos, chunkInfo)
		fmt.Printf("Chunk %d: stored on %v\n", i, successfulNodes)
	}

	meta := &metadata.ObjectMetadata{
		ObjectID:    objectId,
		Bucket:      bucket,
		Key:         key,
		Size:        int64(len(data)),
		Checksum:    CalculateChecksum(data),
		ChunkSize:   s.ChunkSize,
		TotalChunks: totalChunks,
		Chunks:      chunkInfos,
		CreatedAt:   time.Now().UTC(),
		ContentType: contentType,
	}

	if err := s.Metadata.SaveObjectMetadata(meta); err != nil {
		return nil, fmt.Errorf("failed to save metadata: %w", err)
	}

	fmt.Println("Object Stored successfully")
	return meta, nil
}

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
