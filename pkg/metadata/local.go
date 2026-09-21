package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/subhammahanty235/lilio/internal/fsatomic"
)

// objectFileName maps an object key to the file that holds its metadata.
//
// The key is hashed rather than escaped because a filename cannot represent an
// arbitrary key: it is length-limited, may be case-insensitive (as on macOS and
// Windows), and cannot contain a path separator. Any escaping scheme therefore
// has keys it silently maps onto the same file, and two distinct keys sharing a
// file means writing one destroys the other. Hashing has no such collisions in
// practice, at the cost of an opaque filename.
//
// The real key is not lost: it is stored inside the file as ObjectMetadata.Key,
// which is where ListObjects reads it back from.
func objectFileName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]) + ".json"
}

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

func (s *LocalStore) Health(ctx context.Context) error {
	// Check if base path is accessible
	_, err := os.Stat(s.basePath)
	return err
}

// Close closes the store (no-op for local store)
func (s *LocalStore) Close() error {
	return nil
}

func (m *LocalStore) CreateBucket(ctx context.Context, name string) error {
	return m.CreateBucketWithEncryption(ctx, name, EncryptionConfig{Enabled: false})
}

// ----------------------- V2/new code -------------------------------
func (m *LocalStore) CreateBucketWithEncryption(ctx context.Context, name string, encryption EncryptionConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bucketPath := filepath.Join(m.basePath, "buckets", name+".json")

	if _, err := os.Stat(bucketPath); err == nil {
		return fmt.Errorf("%w: %s", ErrBucketExists, name)
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

	if err := fsatomic.WriteFile(bucketPath, data, 0644); err != nil {
		return fmt.Errorf("failed to save bucket metadata: %w", err)
	}

	objectsDir := filepath.Join(m.basePath, "objects", name)
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		return fmt.Errorf("failed to create objects directory: %w", err)
	}

	return nil
}

func (m *LocalStore) GetBucket(ctx context.Context, name string) (*BucketMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bucketPath := filepath.Join(m.basePath, "buckets", name+".json")

	data, err := os.ReadFile(bucketPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrBucketNotFound, name)
		}
		return nil, fmt.Errorf("failed to read bucket metadata: %w", err)
	}

	var bucket BucketMetadata
	if err := json.Unmarshal(data, &bucket); err != nil {
		return nil, fmt.Errorf("failed to parse bucket metadata: %w", err)
	}

	return &bucket, nil
}

func (m *LocalStore) BucketExists(ctx context.Context, name string) bool {
	bucketPath := filepath.Join(m.basePath, "buckets", name+".json")
	_, err := os.Stat(bucketPath)
	return err == nil
}

func (m *LocalStore) IsBucketEncrypted(ctx context.Context, name string) (bool, error) {
	bucket, err := m.GetBucket(ctx, name)
	if err != nil {
		return false, err
	}
	return bucket.Encryption.Enabled, nil
}

func (m *LocalStore) GetBucketEncryption(ctx context.Context, name string) (*EncryptionConfig, error) {
	bucket, err := m.GetBucket(ctx, name)
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

func (m *LocalStore) ListBuckets(ctx context.Context) ([]string, error) {
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

func (m *LocalStore) DeleteBucket(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	objectsDir := filepath.Join(m.basePath, "objects", name)
	entries, _ := os.ReadDir(objectsDir)
	if len(entries) > 0 {
		return fmt.Errorf("%w: %s", ErrBucketNotEmpty, name)
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

func (m *LocalStore) SaveObjectMetadata(ctx context.Context, meta *ObjectMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked(meta, anyRevision)
}

func (m *LocalStore) CompareAndSaveObjectMetadata(ctx context.Context, meta *ObjectMetadata, expectedRevision int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked(meta, expectedRevision)
}

// saveLocked reads the stored revision, checks it against what the caller
// expected, and writes with the revision bumped.
//
// Read-check-write is atomic here only because this store's mutex serialises
// every writer, which holds while a single process owns the directory. Two
// Lilio servers sharing one metadata directory would race; that configuration
// wants etcd, whose transaction does the comparison server-side.
func (m *LocalStore) saveLocked(meta *ObjectMetadata, expectedRevision int64) error {
	objectPath := filepath.Join(m.basePath, "objects", meta.Bucket, objectFileName(meta.Key))

	var current int64
	if existing, err := readObjectFile(objectPath); err == nil {
		current = existing.Revision
	} else if !errors.Is(err, ErrObjectNotFound) {
		return err
	}

	if expectedRevision != anyRevision && current != expectedRevision {
		return fmt.Errorf("%w: %s/%s is at revision %d, expected %d",
			ErrRevisionMismatch, meta.Bucket, meta.Key, current, expectedRevision)
	}

	stored := *meta
	stored.Revision = current + 1

	data, err := json.MarshalIndent(&stored, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal object metadata: %w", err)
	}
	if err := fsatomic.WriteFile(objectPath, data, 0644); err != nil {
		return fmt.Errorf("failed to save object metadata: %w", err)
	}

	meta.Revision = stored.Revision
	return nil
}

// readObjectFile loads one metadata file, reporting a missing file as
// ErrObjectNotFound.
func readObjectFile(path string) (*ObjectMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("failed to read object metadata: %w", err)
	}

	var meta ObjectMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse object metadata: %w", err)
	}
	return &meta, nil
}
func (m *LocalStore) GetObjectMetadata(ctx context.Context, bucket, key string) (*ObjectMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	meta, err := readObjectFile(filepath.Join(m.basePath, "objects", bucket, objectFileName(key)))
	if err != nil {
		if errors.Is(err, ErrObjectNotFound) {
			return nil, fmt.Errorf("%w: %s/%s", ErrObjectNotFound, bucket, key)
		}
		return nil, err
	}
	return meta, nil
}
func (m *LocalStore) DeleteObjectMetadata(ctx context.Context, bucket, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	objectPath := filepath.Join(m.basePath, "objects", bucket, objectFileName(key))

	if err := os.Remove(objectPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s/%s", ErrObjectNotFound, bucket, key)
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

// ListObjects returns one page of a bucket's keys.
//
// Filenames are hashes, so the keys have to come from inside the files - which
// means this reads every object's metadata even to return a single page. The
// pagination bounds the response, not the work. That is acceptable for the
// local store, which is the development backend; etcd answers the same query
// with a bounded range scan.
func (m *LocalStore) ListObjects(ctx context.Context, bucket string, opts ListOptions) (ListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	objectsDir := filepath.Join(m.basePath, "objects", bucket)
	entries, err := os.ReadDir(objectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return ListResult{}, fmt.Errorf("%w: %s", ErrBucketNotFound, bucket)
		}
		return ListResult{}, fmt.Errorf("failed to read objects directory: %w", err)
	}

	var keys []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return ListResult{}, err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue // skips leftover temp files from an interrupted write
		}

		meta, err := readObjectFile(filepath.Join(objectsDir, entry.Name()))
		if err != nil {
			return ListResult{}, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		if opts.Prefix != "" && !strings.HasPrefix(meta.Key, opts.Prefix) {
			continue
		}
		if opts.After != "" && meta.Key <= opts.After {
			continue
		}
		keys = append(keys, meta.Key)
	}

	sort.Strings(keys)
	return paginate(keys, opts.limit()), nil
}

// Ensure LocalStore implements MetadataStore
var _ MetadataStore = (*LocalStore)(nil)
