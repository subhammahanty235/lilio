package metadata

// import (
// 	"encoding/json"
// 	"fmt"
// 	"os"
// 	"path/filepath"
// 	"strings"
// 	"sync"
// 	"time"
// )

// type EncryptionConfig struct {
// 	Enabled   bool   `json:"enabled"`
// 	Algorithm string `json:"algorithm"`
// 	Salt      []byte `json:"salt,omitempty"`
// 	KeyHash   string `json:"key_hash,omitempty"`
// }

// type BucketMetadata struct {
// 	Name       string           `json:"name"`
// 	CreatedAt  time.Time        `json:"created_at"`
// 	Encryption EncryptionConfig `json:"encryption"`
// }

// type ChunkInfo struct {
// 	ChunkID      string   `json:"chunk_id"`
// 	ChunkIndex   int      `json:"chunk_index"`
// 	Size         int64    `json:"size"`
// 	Checksum     string   `json:"checksum"`
// 	StorageNodes []string `json:"storage_nodes"`
// }

// type ObjectMetadata struct {
// 	ObjectID    string      `json:"object_id"`
// 	Bucket      string      `json:"bucket"`
// 	Key         string      `json:"key"`
// 	Size        int64       `json:"size"`
// 	Checksum    string      `json:"checksum"`
// 	ChunkSize   int         `json:"chunk_size"`
// 	TotalChunks int         `json:"total_chunks"`
// 	Chunks      []ChunkInfo `json:"chunks"`
// 	CreatedAt   time.Time   `json:"created_at"`
// 	ContentType string      `json:"content_type"`
// 	Encrypted   bool        `json:"encrypted"`
// }

// type MetadataStore struct {
// 	BasePath    string
// 	BucketsPath string
// 	mu          sync.RWMutex
// }

// func NewMetadataStore(basePath string) (*MetadataStore, error) {
// 	dirs := []string{
// 		basePath,
// 		filepath.Join(basePath, "buckets"),
// 		filepath.Join(basePath, "objects"),
// 	}

// 	for _, dir := range dirs {
// 		if err := os.MkdirAll(dir, 0755); err != nil {
// 			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
// 		}
// 	}

// 	return &MetadataStore{BasePath: basePath}, nil
// }

// // func (m *MetadataStore) CreateBucket(bucketName string) error {
// // 	m.mu.Lock()
// // 	defer m.mu.Unlock()

// // 	bucketPath := filepath.Join(m.BucketsPath, bucketName)

// // 	if _, err := os.Stat(bucketPath); err == nil {
// // 		return fmt.Errorf("bucket already exists: %s", bucketName)
// // 	}

// // 	if err := os.MkdirAll(bucketPath, 0755); err != nil {
// // 		return fmt.Errorf("failed to create bucket: %w", err)
// // 	}

// // 	return nil
// // }

// func (m *MetadataStore) CreateBucket(name string) error {
// 	return m.CreateBucketWithEncryption(name, EncryptionConfig{Enabled: false})
// }

// // ----------------------- V2/new code -------------------------------
// func (m *MetadataStore) CreateBucketWithEncryption(name string, encryption EncryptionConfig) error {
// 	m.mu.Lock()
// 	defer m.mu.Unlock()

// 	bucketPath := filepath.Join(m.BasePath, "buckets", name+".json")

// 	if _, err := os.Stat(bucketPath); err == nil {
// 		return fmt.Errorf("bucket already exists: %s", name)
// 	}

// 	bucket := BucketMetadata{
// 		Name:       name,
// 		CreatedAt:  time.Now().UTC(),
// 		Encryption: encryption,
// 	}

// 	data, err := json.MarshalIndent(bucket, "", "  ")
// 	if err != nil {
// 		return fmt.Errorf("failed to marshal bucket metadata: %w", err)
// 	}

// 	if err := os.WriteFile(bucketPath, data, 0644); err != nil {
// 		return fmt.Errorf("failed to save bucket metadata: %w", err)
// 	}

// 	objectsDir := filepath.Join(m.BasePath, "objects", name)
// 	if err := os.MkdirAll(objectsDir, 0755); err != nil {
// 		return fmt.Errorf("failed to create objects directory: %w", err)
// 	}

// 	return nil
// }

// func (m *MetadataStore) GetBucket(name string) (*BucketMetadata, error) {
// 	m.mu.RLock()
// 	defer m.mu.RUnlock()

// 	bucketPath := filepath.Join(m.BasePath, "buckets", name+".json")

// 	data, err := os.ReadFile(bucketPath)
// 	if err != nil {
// 		if os.IsNotExist(err) {
// 			return nil, fmt.Errorf("bucket not found: %s", name)
// 		}
// 		return nil, fmt.Errorf("failed to read bucket metadata: %w", err)
// 	}

// 	var bucket BucketMetadata
// 	if err := json.Unmarshal(data, &bucket); err != nil {
// 		return nil, fmt.Errorf("failed to parse bucket metadata: %w", err)
// 	}

// 	return &bucket, nil
// }

// func (m *MetadataStore) BucketExists(name string) bool {
// 	bucketPath := filepath.Join(m.BasePath, "buckets", name+".json")
// 	_, err := os.Stat(bucketPath)
// 	return err == nil
// }

// func (m *MetadataStore) IsBucketEncrypted(name string) (bool, error) {
// 	bucket, err := m.GetBucket(name)
// 	if err != nil {
// 		return false, err
// 	}
// 	return bucket.Encryption.Enabled, nil
// }

// func (m *MetadataStore) GetBucketEncryption(name string) (*EncryptionConfig, error) {
// 	bucket, err := m.GetBucket(name)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &bucket.Encryption, nil
// }

// // --------------------<<<============================

// // func (m *MetadataStore) ListBuckets() ([]string, error) {
// // 	m.mu.RLock()
// // 	defer m.mu.RUnlock()

// // 	entries, err := os.ReadDir(m.BucketsPath)
// // 	if err != nil {
// // 		return nil, fmt.Errorf("failed to list buckets: %w", err)
// // 	}

// // 	var buckets []string
// // 	for _, entry := range entries {
// // 		if entry.IsDir() {
// // 			buckets = append(buckets, entry.Name())
// // 		}
// // 	}

// // 	return buckets, nil
// // }

// func (m *MetadataStore) ListBuckets() ([]string, error) {
// 	m.mu.RLock()
// 	defer m.mu.RUnlock()

// 	bucketsDir := filepath.Join(m.BasePath, "buckets")
// 	entries, err := os.ReadDir(bucketsDir)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to read buckets directory: %w", err)
// 	}

// 	var buckets []string
// 	for _, entry := range entries {
// 		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
// 			name := strings.TrimSuffix(entry.Name(), ".json")
// 			buckets = append(buckets, name)
// 		}
// 	}

// 	return buckets, nil
// }

// func (m *MetadataStore) DeleteBucket(name string) error {
// 	m.mu.Lock()
// 	defer m.mu.Unlock()

// 	objectsDir := filepath.Join(m.BasePath, "objects", name)
// 	entries, _ := os.ReadDir(objectsDir)
// 	if len(entries) > 0 {
// 		return fmt.Errorf("bucket not empty: %s", name)
// 	}

// 	os.RemoveAll(objectsDir)

// 	bucketPath := filepath.Join(m.BasePath, "buckets", name+".json")
// 	if err := os.Remove(bucketPath); err != nil {
// 		return fmt.Errorf("failed to delete bucket: %w", err)
// 	}

// 	return nil
// }

// // func (m *MetadataStore) SaveObjectMetadata(meta *ObjectMetadata) error {
// // 	m.mu.Lock()
// // 	defer m.mu.Unlock()

// // 	bucketPath := filepath.Join(m.BucketsPath, meta.Bucket)
// // 	if _, err := os.Stat(bucketPath); os.IsNotExist(err) {
// // 		return fmt.Errorf("bucket not found: %s", meta.Bucket)
// // 	}

// // 	// Create safe filename from key
// // 	safeKey := strings.ReplaceAll(meta.Key, "/", "_")
// // 	metaFile := filepath.Join(bucketPath, safeKey+".json")

// // 	data, err := json.MarshalIndent(meta, "", "  ")
// // 	if err != nil {
// // 		return fmt.Errorf("failed to marshal metadata: %w", err)
// // 	}

// // 	if err := os.WriteFile(metaFile, data, 0644); err != nil {
// // 		return fmt.Errorf("failed to save metadata: %w", err)
// // 	}

// // 	return nil
// // }

// func (m *MetadataStore) SaveObjectMetadata(meta *ObjectMetadata) error {
// 	m.mu.Lock()
// 	defer m.mu.Unlock()

// 	safeKey := strings.ReplaceAll(meta.Key, "/", "_")
// 	objectPath := filepath.Join(m.BasePath, "objects", meta.Bucket, safeKey+".json")

// 	data, err := json.MarshalIndent(meta, "", "  ")
// 	if err != nil {
// 		return fmt.Errorf("failed to marshal object metadata: %w", err)
// 	}

// 	if err := os.WriteFile(objectPath, data, 0644); err != nil {
// 		return fmt.Errorf("failed to save object metadata: %w", err)
// 	}

// 	return nil
// }

// // func (m *MetadataStore) GetObjectMetadata(bucket, key string) (*ObjectMetadata, error) {
// // 	m.mu.RLock()
// // 	defer m.mu.RUnlock()

// // 	safeKey := strings.ReplaceAll(key, "/", "_")
// // 	metaFile := filepath.Join(m.BucketsPath, bucket, safeKey+".json")

// // 	data, err := os.ReadFile(metaFile)
// // 	if err != nil {
// // 		if os.IsNotExist(err) {
// // 			return nil, fmt.Errorf("object not found: %s/%s", bucket, key)
// // 		}
// // 		return nil, fmt.Errorf("failed to read metadata: %w", err)
// // 	}

// // 	var meta ObjectMetadata
// // 	if err := json.Unmarshal(data, &meta); err != nil {
// // 		return nil, fmt.Errorf("failed to parse metadata: %w", err)
// // 	}

// // 	return &meta, nil
// // }

// func (m *MetadataStore) GetObjectMetadata(bucket, key string) (*ObjectMetadata, error) {
// 	m.mu.RLock()
// 	defer m.mu.RUnlock()

// 	safeKey := strings.ReplaceAll(key, "/", "_")
// 	objectPath := filepath.Join(m.BasePath, "objects", bucket, safeKey+".json")

// 	data, err := os.ReadFile(objectPath)
// 	if err != nil {
// 		if os.IsNotExist(err) {
// 			return nil, fmt.Errorf("object not found: %s/%s", bucket, key)
// 		}
// 		return nil, fmt.Errorf("failed to read object metadata: %w", err)
// 	}

// 	var meta ObjectMetadata
// 	if err := json.Unmarshal(data, &meta); err != nil {
// 		return nil, fmt.Errorf("failed to parse object metadata: %w", err)
// 	}

// 	return &meta, nil
// }
// func (m *MetadataStore) DeleteObjectMetadata(bucket, key string) error {
// 	m.mu.Lock()
// 	defer m.mu.Unlock()

// 	safeKey := strings.ReplaceAll(key, "/", "_")
// 	objectPath := filepath.Join(m.BasePath, "objects", bucket, safeKey+".json")

// 	if err := os.Remove(objectPath); err != nil {
// 		if os.IsNotExist(err) {
// 			return fmt.Errorf("object not found: %s/%s", bucket, key)
// 		}
// 		return fmt.Errorf("failed to delete metadata: %w", err)
// 	}

// 	return nil
// }

// // func (m *MetadataStore) ListObjects(bucket, prefix string) ([]string, error) {
// // 	m.mu.RLock()
// // 	defer m.mu.RUnlock()

// // 	bucketPath := filepath.Join(m.BucketsPath, bucket)

// // 	entries, err := os.ReadDir(bucketPath)
// // 	if err != nil {
// // 		return nil, fmt.Errorf("bucket not found: %s", bucket)
// // 	}

// // 	var objects []string
// // 	for _, entry := range entries {
// // 		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
// // 			key := strings.TrimSuffix(entry.Name(), ".json")
// // 			key = strings.ReplaceAll(key, "_", "/")

// // 			if strings.HasPrefix(key, prefix) {
// // 				objects = append(objects, key)
// // 			}
// // 		}
// // 	}

// // 	return objects, nil
// // }

// func (m *MetadataStore) ListObjects(bucket, prefix string) ([]string, error) {
// 	m.mu.RLock()
// 	defer m.mu.RUnlock()

// 	objectsDir := filepath.Join(m.BasePath, "objects", bucket)
// 	entries, err := os.ReadDir(objectsDir)
// 	if err != nil {
// 		if os.IsNotExist(err) {
// 			return nil, fmt.Errorf("bucket not found: %s", bucket)
// 		}
// 		return nil, fmt.Errorf("failed to read objects directory: %w", err)
// 	}

// 	var objects []string
// 	for _, entry := range entries {
// 		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
// 			key := strings.TrimSuffix(entry.Name(), ".json")
// 			key = strings.ReplaceAll(key, "_", "/")

// 			if prefix == "" || strings.HasPrefix(key, prefix) {
// 				objects = append(objects, key)
// 			}
// 		}
// 	}

// 	return objects, nil
// }
