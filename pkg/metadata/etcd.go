package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type EtcdStore struct {
	client  *clientv3.Client
	prefix  string
	timeout time.Duration
}

func NewEtcdStore(cfg *EtcdConfig) (*EtcdStore, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("at least one etcd endpoint is required")
	}

	timeout := cfg.DialTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "/lilio"
	}

	// Create etcd client
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: timeout,
		Username:    cfg.Username,
		Password:    cfg.Password,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}

	store := &EtcdStore{
		client:  client,
		prefix:  prefix,
		timeout: timeout,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	_, err = client.Status(ctx, cfg.Endpoints[0])
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("etcd health check failed: %w", err)
	}

	return store, nil
}

// Health checks if etcd is reachable
func (s *EtcdStore) Health() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	_, err := s.client.MemberList(ctx)
	return err
}

func (s *EtcdStore) Close() error {
	return s.client.Close()
}

func (s *EtcdStore) Type() string {
	return string(StoreTypeEtcd)
}

func (s *EtcdStore) bucketKey(name string) string {
	return fmt.Sprintf("%s/buckets/%s", s.prefix, name)
}

func (s *EtcdStore) objectKey(bucket, key string) string {
	// Replace / in key with : to avoid conflicts
	safeKey := strings.ReplaceAll(key, "/", ":")
	return fmt.Sprintf("%s/objects/%s/%s", s.prefix, bucket, safeKey)
}

func (s *EtcdStore) objectsPrefix(bucket string) string {
	return fmt.Sprintf("%s/objects/%s/", s.prefix, bucket)
}

func (s *EtcdStore) bucketsPrefix() string {
	return fmt.Sprintf("%s/buckets/", s.prefix)
}

func (s *EtcdStore) CreateBucket(name string) error {
	return s.CreateBucketWithEncryption(name, EncryptionConfig{Enabled: false})
}

func (s *EtcdStore) CreateBucketWithEncryption(name string, encryption EncryptionConfig) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	key := s.bucketKey(name)

	// Check if already exists
	resp, err := s.client.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to check bucket: %w", err)
	}
	if len(resp.Kvs) > 0 {
		return fmt.Errorf("bucket already exists: %s", name)
	}

	// Create bucket metadata
	bucket := BucketMetadata{
		Name:       name,
		CreatedAt:  time.Now().UTC(),
		Encryption: encryption,
	}

	data, err := json.Marshal(bucket)
	if err != nil {
		return fmt.Errorf("failed to marshal bucket: %w", err)
	}

	// Put with transaction (atomic create)
	txn := s.client.Txn(ctx)
	txn = txn.If(clientv3.Compare(clientv3.Version(key), "=", 0))
	txn = txn.Then(clientv3.OpPut(key, string(data)))

	txnResp, err := txn.Commit()
	if err != nil {
		return fmt.Errorf("failed to create bucket: %w", err)
	}

	if !txnResp.Succeeded {
		return fmt.Errorf("bucket already exists: %s", name)
	}

	return nil
}

func (s *EtcdStore) GetBucket(name string) (*BucketMetadata, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	resp, err := s.client.Get(ctx, s.bucketKey(name))
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("bucket not found: %s", name)
	}

	var bucket BucketMetadata
	if err := json.Unmarshal(resp.Kvs[0].Value, &bucket); err != nil {
		return nil, fmt.Errorf("failed to parse bucket: %w", err)
	}

	return &bucket, nil
}

func (s *EtcdStore) BucketExists(name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	resp, err := s.client.Get(ctx, s.bucketKey(name))
	if err != nil {
		return false
	}
	return len(resp.Kvs) > 0
}

func (s *EtcdStore) IsBucketEncrypted(name string) (bool, error) {
	bucket, err := s.GetBucket(name)
	if err != nil {
		return false, err
	}
	return bucket.Encryption.Enabled, nil
}

func (s *EtcdStore) GetBucketEncryption(name string) (*EncryptionConfig, error) {
	bucket, err := s.GetBucket(name)
	if err != nil {
		return nil, err
	}
	return &bucket.Encryption, nil
}

func (s *EtcdStore) ListBuckets() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	resp, err := s.client.Get(ctx, s.bucketsPrefix(), clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to list buckets: %w", err)
	}

	buckets := make([]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		// Extract bucket name from key
		key := string(kv.Key)
		name := strings.TrimPrefix(key, s.bucketsPrefix())
		buckets = append(buckets, name)
	}

	return buckets, nil
}

func (s *EtcdStore) DeleteBucket(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	// Check if bucket has objects
	objectsResp, err := s.client.Get(ctx, s.objectsPrefix(name), clientv3.WithPrefix(), clientv3.WithLimit(1))
	if err != nil {
		return fmt.Errorf("failed to check bucket objects: %w", err)
	}
	if len(objectsResp.Kvs) > 0 {
		return fmt.Errorf("bucket not empty: %s", name)
	}

	// Delete bucket
	_, err = s.client.Delete(ctx, s.bucketKey(name))
	if err != nil {
		return fmt.Errorf("failed to delete bucket: %w", err)
	}

	return nil
}

func (s *EtcdStore) SaveObjectMetadata(meta *ObjectMetadata) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal object: %w", err)
	}

	key := s.objectKey(meta.Bucket, meta.Key)
	_, err = s.client.Put(ctx, key, string(data))
	if err != nil {
		return fmt.Errorf("failed to save object: %w", err)
	}

	return nil
}

func (s *EtcdStore) GetObjectMetadata(bucket, key string) (*ObjectMetadata, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	resp, err := s.client.Get(ctx, s.objectKey(bucket, key))
	if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("object not found: %s/%s", bucket, key)
	}

	var meta ObjectMetadata
	if err := json.Unmarshal(resp.Kvs[0].Value, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse object: %w", err)
	}

	return &meta, nil
}

func (s *EtcdStore) DeleteObjectMetadata(bucket, key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	_, err := s.client.Delete(ctx, s.objectKey(bucket, key))
	if err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}

	return nil
}

func (s *EtcdStore) ListObjects(bucket, prefix string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	// Check bucket exists
	if !s.BucketExists(bucket) {
		return nil, fmt.Errorf("bucket not found: %s", bucket)
	}

	searchPrefix := s.objectsPrefix(bucket)
	resp, err := s.client.Get(ctx, searchPrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to list objects: %w", err)
	}

	objects := make([]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		// Extract object key from etcd key
		etcdKey := string(kv.Key)
		objectKey := strings.TrimPrefix(etcdKey, searchPrefix)
		// Convert back from : to /
		objectKey = strings.ReplaceAll(objectKey, ":", "/")

		// Apply prefix filter
		if prefix == "" || strings.HasPrefix(objectKey, prefix) {
			objects = append(objects, objectKey)
		}
	}

	return objects, nil
}

var _ MetadataStore = (*EtcdStore)(nil)
