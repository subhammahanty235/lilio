package metadata

import (
	"context"
	"fmt"
	"sort"
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

func (s *MemoryStore) Health(ctx context.Context) error {
	return nil
}

func (s *MemoryStore) Close() error {
	return nil
}

func (s *MemoryStore) CreateBucket(ctx context.Context, name string) error {
	return s.CreateBucketWithEncryption(ctx, name, EncryptionConfig{Enabled: false})
}

func (s *MemoryStore) CreateBucketWithEncryption(ctx context.Context, name string, encryption EncryptionConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.buckets[name]; exists {
		return fmt.Errorf("%w: %s", ErrBucketExists, name)
	}

	s.buckets[name] = &BucketMetadata{
		Name:       name,
		CreatedAt:  time.Now().UTC(),
		Encryption: encryption,
	}

	return nil
}

func (s *MemoryStore) GetBucket(ctx context.Context, name string) (*BucketMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	bucket, exists := s.buckets[name]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrBucketNotFound, name)
	}

	return bucket, nil
}

func (s *MemoryStore) BucketExists(ctx context.Context, name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.buckets[name]
	return exists
}

func (s *MemoryStore) IsBucketEncrypted(ctx context.Context, name string) (bool, error) {
	bucket, err := s.GetBucket(ctx, name)
	if err != nil {
		return false, err
	}
	return bucket.Encryption.Enabled, nil
}

func (s *MemoryStore) GetBucketEncryption(ctx context.Context, name string) (*EncryptionConfig, error) {
	bucket, err := s.GetBucket(ctx, name)
	if err != nil {
		return nil, err
	}
	return &bucket.Encryption, nil
}

func (s *MemoryStore) ListBuckets(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	buckets := make([]string, 0, len(s.buckets))
	for name := range s.buckets {
		buckets = append(buckets, name)
	}
	return buckets, nil
}

func (s *MemoryStore) DeleteBucket(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if bucket has objects
	prefix := name + "/"
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			return fmt.Errorf("%w: %s", ErrBucketNotEmpty, name)
		}
	}

	if _, exists := s.buckets[name]; !exists {
		return fmt.Errorf("%w: %s", ErrBucketNotFound, name)
	}

	delete(s.buckets, name)
	return nil
}

// ==================== Object Operations ====================

func (s *MemoryStore) SaveObjectMetadata(ctx context.Context, meta *ObjectMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(meta, anyRevision)
}

func (s *MemoryStore) CompareAndSaveObjectMetadata(ctx context.Context, meta *ObjectMetadata, expectedRevision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(meta, expectedRevision)
}

// saveLocked stores a copy of meta, bumping its revision. The caller's meta is
// updated with the revision that was assigned, so it can be used for a later
// conditional write.
func (s *MemoryStore) saveLocked(meta *ObjectMetadata, expectedRevision int64) error {
	key := fmt.Sprintf("%s/%s", meta.Bucket, meta.Key)

	var current int64
	if existing, ok := s.objects[key]; ok {
		current = existing.Revision
	}
	if expectedRevision != anyRevision && current != expectedRevision {
		return fmt.Errorf("%w: %s/%s is at revision %d, expected %d",
			ErrRevisionMismatch, meta.Bucket, meta.Key, current, expectedRevision)
	}

	stored := *meta
	stored.Revision = current + 1
	s.objects[key] = &stored
	meta.Revision = stored.Revision
	return nil
}

func (s *MemoryStore) GetObjectMetadata(ctx context.Context, bucket, key string) (*ObjectMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	fullKey := fmt.Sprintf("%s/%s", bucket, key)
	meta, exists := s.objects[fullKey]
	if !exists {
		return nil, fmt.Errorf("%w: %s/%s", ErrObjectNotFound, bucket, key)
	}

	// A copy, so a caller mutating what it read cannot alter the store.
	out := *meta
	return &out, nil
}

func (s *MemoryStore) DeleteObjectMetadata(ctx context.Context, bucket, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	fullKey := fmt.Sprintf("%s/%s", bucket, key)
	if _, exists := s.objects[fullKey]; !exists {
		return fmt.Errorf("%w: %s/%s", ErrObjectNotFound, bucket, key)
	}
	delete(s.objects, fullKey)
	return nil
}

func (s *MemoryStore) ListObjects(ctx context.Context, bucket string, opts ListOptions) (ListResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, exists := s.buckets[bucket]; !exists {
		return ListResult{}, fmt.Errorf("%w: %s", ErrBucketNotFound, bucket)
	}

	bucketPrefix := bucket + "/"
	var keys []string
	for fullKey := range s.objects {
		if !strings.HasPrefix(fullKey, bucketPrefix) {
			continue
		}
		key := strings.TrimPrefix(fullKey, bucketPrefix)
		if opts.Prefix != "" && !strings.HasPrefix(key, opts.Prefix) {
			continue
		}
		if opts.After != "" && key <= opts.After {
			continue
		}
		keys = append(keys, key)
	}

	sort.Strings(keys)
	return paginate(keys, opts.limit()), nil
}

var _ MetadataStore = (*MemoryStore)(nil)
