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

func (m *MetadataStore) GetObjectMetadata(bucket, key string) (*ObjectMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	safeKey := strings.ReplaceAll(key, "/", "_")
	metaFile := filepath.Join(m.BucketsPath, bucket, safeKey+".json")

	data, err := os.ReadFile(metaFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("object not found: %s/%s", bucket, key)
		}
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	var meta ObjectMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	return &meta, nil
}

func (m *MetadataStore) DeleteObjectMetadata(bucket, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	safeKey := strings.ReplaceAll(key, "/", "_")
	metaFile := filepath.Join(m.BucketsPath, bucket, safeKey+".json")

	if err := os.Remove(metaFile); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("object not found: %s/%s", bucket, key)
		}
		return fmt.Errorf("failed to delete metadata: %w", err)
	}

	return nil
}

func (m *MetadataStore) ListObjects(bucket, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bucketPath := filepath.Join(m.BucketsPath, bucket)

	entries, err := os.ReadDir(bucketPath)
	if err != nil {
		return nil, fmt.Errorf("bucket not found: %s", bucket)
	}

	var objects []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			key := strings.TrimSuffix(entry.Name(), ".json")
			key = strings.ReplaceAll(key, "_", "/")

			if strings.HasPrefix(key, prefix) {
				objects = append(objects, key)
			}
		}
	}

	return objects, nil
}
