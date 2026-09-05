package metadata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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

// writeFileAtomic writes data to path so that a concurrent or post-crash reader
// sees either the previous contents or the complete new contents, never a
// half-written file.
//
// os.WriteFile truncates first and then writes, so a crash in between leaves a
// short or empty file - and for metadata that means an object whose chunks are
// all intact becomes permanently unreadable. Writing to a temporary file and
// renaming avoids that: rename(2) is atomic within a filesystem.
//
// The two fsyncs serve a different purpose from the rename. Rename gives
// atomicity (no torn state); fsync gives durability (the bytes, and then the
// rename itself, actually reach the disk rather than sitting in the page cache).
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below has succeeded

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to set permissions: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to commit file: %w", err)
	}

	// Persist the rename itself, not just the file contents.
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("failed to open directory for sync: %w", err)
	}
	defer d.Close()
	return d.Sync()
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

	if err := writeFileAtomic(bucketPath, data, 0644); err != nil {
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

func (m *LocalStore) SaveObjectMetadata(meta *ObjectMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	objectPath := filepath.Join(m.basePath, "objects", meta.Bucket, objectFileName(meta.Key))

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal object metadata: %w", err)
	}

	if err := writeFileAtomic(objectPath, data, 0644); err != nil {
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
// 			return nil, fmt.Errorf("%w: %s/%s", ErrObjectNotFound, bucket, key)
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

	objectPath := filepath.Join(m.basePath, "objects", bucket, objectFileName(key))

	data, err := os.ReadFile(objectPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s/%s", ErrObjectNotFound, bucket, key)
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

	// Filenames are hashes, so the key has to come from inside each file.
	// That makes listing O(objects) reads on this backend; acceptable because
	// the local store is the development backend, while etcd - the backend
	// meant for real use - answers the same query with one range scan.
	var objects []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue // skips leftover .tmp-* files from an interrupted write
		}

		data, err := os.ReadFile(filepath.Join(objectsDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read object metadata %s: %w", entry.Name(), err)
		}

		var meta ObjectMetadata
		if err := json.Unmarshal(data, &meta); err != nil {
			return nil, fmt.Errorf("failed to parse object metadata %s: %w", entry.Name(), err)
		}

		if prefix == "" || strings.HasPrefix(meta.Key, prefix) {
			objects = append(objects, meta.Key)
		}
	}

	return objects, nil
}

// Ensure LocalStore implements MetadataStore
var _ MetadataStore = (*LocalStore)(nil)
