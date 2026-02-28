package metadata

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// in-memory maps useful for testing and developments
type MemoryStore struct {
	buckets map[string]*BucketMetadata
	objects map[string]*ObjectMetadata
	mu      sync.RWMutex
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		buckets: make(map[string]*BucketMetadata),
		objects: make(map[string]*ObjectMetadata),
	}
}

func (s *MemoryStore) Type() string {
	return string(StoreTypeMemory)
}

func (s *MemoryStore) Health() error {
	return nil
}

func (s *MemoryStore) Close() error {
	return nil
}

func (s *MemoryStore) CreateBucket(name string) error {
	return s.CreateBucketWithEncryption(name, EncryptionConfig{Enabled: false})
}

func (s *MemoryStore) CreateBucketWithEncryption(name string, encryption EncryptionConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.buckets[name]; exists {
		return fmt.Errorf("bucket already exists: %s", name)
	}

	s.buckets[name] = &BucketMetadata{
		Name:       name,
		CreatedAt:  time.Now().UTC(),
		Encryption: encryption,
	}

	return nil
}

func (s *MemoryStore) GetBucket(name string) (*BucketMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	bucket, exists := s.buckets[name]
	if !exists {
		return nil, fmt.Errorf("bucket not found: %s", name)
	}

	return bucket, nil
}

func (s *MemoryStore) BucketExists(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.buckets[name]
	return exists
}

func (s *MemoryStore) IsBucketEncrypted(name string) (bool, error) {
	bucket, err := s.GetBucket(name)
	if err != nil {
		return false, err
	}
	return bucket.Encryption.Enabled, nil
}

func (s *MemoryStore) GetBucketEncryption(name string) (*EncryptionConfig, error) {
	bucket, err := s.GetBucket(name)
	if err != nil {
		return nil, err
	}
	return &bucket.Encryption, nil
}

func (s *MemoryStore) ListBuckets() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	buckets := make([]string, 0, len(s.buckets))
	for name := range s.buckets {
		buckets = append(buckets, name)
	}
	return buckets, nil
}

func (s *MemoryStore) DeleteBucket(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if bucket has objects
	prefix := name + "/"
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			return fmt.Errorf("bucket not empty: %s", name)
		}
	}

	if _, exists := s.buckets[name]; !exists {
		return fmt.Errorf("bucket not found: %s", name)
	}

	delete(s.buckets, name)
	return nil
}

// ==================== Object Operations ====================

func (s *MemoryStore) SaveObjectMetadata(meta *ObjectMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := fmt.Sprintf("%s/%s", meta.Bucket, meta.Key)
	s.objects[key] = meta
	return nil
}

func (s *MemoryStore) GetObjectMetadata(bucket, key string) (*ObjectMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	fullKey := fmt.Sprintf("%s/%s", bucket, key)
	meta, exists := s.objects[fullKey]
	if !exists {
		return nil, fmt.Errorf("object not found: %s/%s", bucket, key)
	}

	return meta, nil
}

func (s *MemoryStore) DeleteObjectMetadata(bucket, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	fullKey := fmt.Sprintf("%s/%s", bucket, key)
	delete(s.objects, fullKey)
	return nil
}

func (s *MemoryStore) ListObjects(bucket, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Check bucket exists
	if _, exists := s.buckets[bucket]; !exists {
		return nil, fmt.Errorf("bucket not found: %s", bucket)
	}

	bucketPrefix := bucket + "/"
	var objects []string

	for fullKey := range s.objects {
		if strings.HasPrefix(fullKey, bucketPrefix) {
			key := strings.TrimPrefix(fullKey, bucketPrefix)
			if prefix == "" || strings.HasPrefix(key, prefix) {
				objects = append(objects, key)
			}
		}
	}

	return objects, nil
}

var _ MetadataStore = (*MemoryStore)(nil)
