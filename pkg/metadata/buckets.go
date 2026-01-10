package metadata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ChunkInfo struct {
	ChunkID      string   `json:"chunk_id"`
	ChunkIndex   int      `json:"chunk_index"`
	Size         int64    `json:"size"`
	Checksum     string   `json:"checksum"`
	StorageNodes []string `json:"storage_nodes"`
}

type ObjectMetadata struct {
	ObjectID    string      `json:"object_id"`
	Bucket      string      `json:"bucket"`
	Key         string      `json:"key"`
	Size        int64       `json:"size"`
	Checksum    string      `json:"checksum"`
	ChunkSize   int         `json:"chunk_size"`
	TotalChunks int         `json:"total_chunks"`
	Chunks      []ChunkInfo `json:"chunks"`
	CreatedAt   time.Time   `json:"created_at"`
	ContentType string      `json:"content_type"`
}

type MetadataStore struct {
	BasePath    string
	BucketsPath string
	mu          sync.RWMutex
}

func NewMetadataStore(basePath string) (*MetadataStore, error) {
	bucketsPath := filepath.Join(basePath, "buckets")

	if err := os.MkdirAll(bucketsPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create metadata path: %w", err)
	}

	return &MetadataStore{
		BasePath:    basePath,
		BucketsPath: bucketsPath,
	}, nil
}

func (m *MetadataStore) CreateBucket(bucketName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bucketPath := filepath.Join(m.BucketsPath, bucketName)

	if _, err := os.Stat(bucketPath); err == nil {
		return fmt.Errorf("bucket already exists: %s", bucketName)
	}

	if err := os.MkdirAll(bucketPath, 0755); err != nil {
		return fmt.Errorf("failed to create bucket: %w", err)
	}

	return nil
}

func (m *MetadataStore) ListBuckets() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries, err := os.ReadDir(m.BucketsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list buckets: %w", err)
	}

	var buckets []string
	for _, entry := range entries {
		if entry.IsDir() {
			buckets = append(buckets, entry.Name())
		}
	}

	return buckets, nil
}

func (m *MetadataStore) SaveObjectMetadata(meta *ObjectMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bucketPath := filepath.Join(m.BucketsPath, meta.Bucket)
	if _, err := os.Stat(bucketPath); os.IsNotExist(err) {
		return fmt.Errorf("bucket not found: %s", meta.Bucket)
	}

	// Create safe filename from key
	safeKey := strings.ReplaceAll(meta.Key, "/", "_")
	metaFile := filepath.Join(bucketPath, safeKey+".json")

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(metaFile, data, 0644); err != nil {
		return fmt.Errorf("failed to save metadata: %w", err)
	}

	return nil
}
