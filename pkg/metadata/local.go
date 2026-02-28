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

type LocalStore struct {
	basePath string
	mu       sync.RWMutex
}

func NewLocalStore(basePath string) (*LocalStore, error) {
	dirs := []string{
		basePath,
		filepath.Join(basePath, "buckets"),
		filepath.Join(basePath, "objects"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return &LocalStore{basePath: basePath}, nil
}

func (s *LocalStore) Type() string {
	return string(StoreTypeLocal)
}

func (s *LocalStore) Health() error {
	// Check if base path is accessible
	_, err := os.Stat(s.basePath)
	return err
}

// Close closes the store (no-op for local store)
func (s *LocalStore) Close() error {
	return nil
}

func (m *LocalStore) CreateBucket(name string) error {
	return m.CreateBucketWithEncryption(name, EncryptionConfig{Enabled: false})
}

// ----------------------- V2/new code -------------------------------
func (m *LocalStore) CreateBucketWithEncryption(name string, encryption EncryptionConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bucketPath := filepath.Join(m.basePath, "buckets", name+".json")

	if _, err := os.Stat(bucketPath); err == nil {
		return fmt.Errorf("bucket already exists: %s", name)
	}

	bucket := BucketMetadata{
		Name:       name,
		CreatedAt:  time.Now().UTC(),
		Encryption: encryption,
	}

	data, err := json.MarshalIndent(bucket, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal bucket metadata: %w", err)
	}

	if err := os.WriteFile(bucketPath, data, 0644); err != nil {
		return fmt.Errorf("failed to save bucket metadata: %w", err)
	}

	objectsDir := filepath.Join(m.basePath, "objects", name)
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		return fmt.Errorf("failed to create objects directory: %w", err)
	}

	return nil
}

func (m *LocalStore) GetBucket(name string) (*BucketMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bucketPath := filepath.Join(m.basePath, "buckets", name+".json")

	data, err := os.ReadFile(bucketPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("bucket not found: %s", name)
		}
		return nil, fmt.Errorf("failed to read bucket metadata: %w", err)
	}

	var bucket BucketMetadata
	if err := json.Unmarshal(data, &bucket); err != nil {
		return nil, fmt.Errorf("failed to parse bucket metadata: %w", err)
	}

	return &bucket, nil
}

func (m *LocalStore) BucketExists(name string) bool {
	bucketPath := filepath.Join(m.basePath, "buckets", name+".json")
	_, err := os.Stat(bucketPath)
	return err == nil
}

func (m *LocalStore) IsBucketEncrypted(name string) (bool, error) {
	bucket, err := m.GetBucket(name)
	if err != nil {
		return false, err
	}
	return bucket.Encryption.Enabled, nil
}

func (m *LocalStore) GetBucketEncryption(name string) (*EncryptionConfig, error) {
	bucket, err := m.GetBucket(name)
	if err != nil {
		return nil, err
	}
	return &bucket.Encryption, nil
}

// --------------------<<<============================

// func (m *LocalStore) ListBuckets() ([]string, error) {
// 	m.mu.RLock()
// 	defer m.mu.RUnlock()

// 	entries, err := os.ReadDir(m.BucketsPath)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to list buckets: %w", err)
// 	}

// 	var buckets []string
// 	for _, entry := range entries {
// 		if entry.IsDir() {
// 			buckets = append(buckets, entry.Name())
// 		}
// 	}

// 	return buckets, nil
// }

func (m *LocalStore) ListBuckets() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bucketsDir := filepath.Join(m.basePath, "buckets")
	entries, err := os.ReadDir(bucketsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read buckets directory: %w", err)
	}

	var buckets []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			name := strings.TrimSuffix(entry.Name(), ".json")
			buckets = append(buckets, name)
		}
	}

	return buckets, nil
}

func (m *LocalStore) DeleteBucket(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	objectsDir := filepath.Join(m.basePath, "objects", name)
	entries, _ := os.ReadDir(objectsDir)
	if len(entries) > 0 {
		return fmt.Errorf("bucket not empty: %s", name)
	}

	os.RemoveAll(objectsDir)

	bucketPath := filepath.Join(m.basePath, "buckets", name+".json")
	if err := os.Remove(bucketPath); err != nil {
		return fmt.Errorf("failed to delete bucket: %w", err)
	}

	return nil
}

// func (m *LocalStore) SaveObjectMetadata(meta *ObjectMetadata) error {
// 	m.mu.Lock()
// 	defer m.mu.Unlock()

// 	bucketPath := filepath.Join(m.BucketsPath, meta.Bucket)
// 	if _, err := os.Stat(bucketPath); os.IsNotExist(err) {
// 		return fmt.Errorf("bucket not found: %s", meta.Bucket)
// 	}

// 	// Create safe filename from key
// 	safeKey := strings.ReplaceAll(meta.Key, "/", "_")
// 	metaFile := filepath.Join(bucketPath, safeKey+".json")

// 	data, err := json.MarshalIndent(meta, "", "  ")
// 	if err != nil {
// 		return fmt.Errorf("failed to marshal metadata: %w", err)
// 	}

// 	if err := os.WriteFile(metaFile, data, 0644); err != nil {
// 		return fmt.Errorf("failed to save metadata: %w", err)
// 	}

// 	return nil
// }

func (m *LocalStore) SaveObjectMetadata(meta *ObjectMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	safeKey := strings.ReplaceAll(meta.Key, "/", "_")
	objectPath := filepath.Join(m.basePath, "objects", meta.Bucket, safeKey+".json")

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal object metadata: %w", err)
	}

	if err := os.WriteFile(objectPath, data, 0644); err != nil {
		return fmt.Errorf("failed to save object metadata: %w", err)
	}

	return nil
}

// func (m *LocalStore) GetObjectMetadata(bucket, key string) (*ObjectMetadata, error) {
// 	m.mu.RLock()
// 	defer m.mu.RUnlock()

// 	safeKey := strings.ReplaceAll(key, "/", "_")
// 	metaFile := filepath.Join(m.BucketsPath, bucket, safeKey+".json")

// 	data, err := os.ReadFile(metaFile)
// 	if err != nil {
// 		if os.IsNotExist(err) {
// 			return nil, fmt.Errorf("object not found: %s/%s", bucket, key)
// 		}
// 		return nil, fmt.Errorf("failed to read metadata: %w", err)
// 	}

// 	var meta ObjectMetadata
// 	if err := json.Unmarshal(data, &meta); err != nil {
// 		return nil, fmt.Errorf("failed to parse metadata: %w", err)
// 	}

// 	return &meta, nil
// }

func (m *LocalStore) GetObjectMetadata(bucket, key string) (*ObjectMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	safeKey := strings.ReplaceAll(key, "/", "_")
	objectPath := filepath.Join(m.basePath, "objects", bucket, safeKey+".json")

	data, err := os.ReadFile(objectPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("object not found: %s/%s", bucket, key)
		}
		return nil, fmt.Errorf("failed to read object metadata: %w", err)
	}

	var meta ObjectMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse object metadata: %w", err)
	}

	return &meta, nil
}
func (m *LocalStore) DeleteObjectMetadata(bucket, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	safeKey := strings.ReplaceAll(key, "/", "_")
	objectPath := filepath.Join(m.basePath, "objects", bucket, safeKey+".json")

	if err := os.Remove(objectPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("object not found: %s/%s", bucket, key)
		}
		return fmt.Errorf("failed to delete metadata: %w", err)
	}

	return nil
}

// func (m *LocalStore) ListObjects(bucket, prefix string) ([]string, error) {
// 	m.mu.RLock()
// 	defer m.mu.RUnlock()

// 	bucketPath := filepath.Join(m.BucketsPath, bucket)

// 	entries, err := os.ReadDir(bucketPath)
// 	if err != nil {
// 		return nil, fmt.Errorf("bucket not found: %s", bucket)
// 	}

// 	var objects []string
// 	for _, entry := range entries {
// 		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
// 			key := strings.TrimSuffix(entry.Name(), ".json")
// 			key = strings.ReplaceAll(key, "_", "/")

// 			if strings.HasPrefix(key, prefix) {
// 				objects = append(objects, key)
// 			}
// 		}
// 	}

// 	return objects, nil
// }

func (m *LocalStore) ListObjects(bucket, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	objectsDir := filepath.Join(m.basePath, "objects", bucket)
	entries, err := os.ReadDir(objectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("bucket not found: %s", bucket)
		}
		return nil, fmt.Errorf("failed to read objects directory: %w", err)
	}

	var objects []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			key := strings.TrimSuffix(entry.Name(), ".json")
			key = strings.ReplaceAll(key, "_", "/")

			if prefix == "" || strings.HasPrefix(key, prefix) {
				objects = append(objects, key)
			}
		}
	}

	return objects, nil
}

// Ensure LocalStore implements MetadataStore
var _ MetadataStore = (*LocalStore)(nil)
