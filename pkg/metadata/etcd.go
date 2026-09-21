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

// withTimeout bounds a call at the store's configured timeout, leaving a
// caller that already has a shorter deadline alone.
func (s *EtcdStore) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= s.timeout {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.timeout)
}

// Health checks if etcd is reachable
func (s *EtcdStore) Health(ctx context.Context) error {
	ctx, cancel := s.withTimeout(ctx)
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

// objectKey builds the etcd key for an object.
//
// The object key is appended verbatim. etcd keys are opaque byte strings, so a
// "/" inside an object key needs no escaping: a prefix scan of objectsPrefix
// still returns the object, and trimming that prefix recovers the key exactly.
// The previous "/" -> ":" substitution was not only unnecessary but lossy, as
// it made the keys "a/b" and "a:b" the same object.
func (s *EtcdStore) objectKey(bucket, key string) string {
	return s.objectsPrefix(bucket) + key
}

func (s *EtcdStore) objectsPrefix(bucket string) string {
	return fmt.Sprintf("%s/objects/%s/", s.prefix, bucket)
}

func (s *EtcdStore) bucketsPrefix() string {
	return fmt.Sprintf("%s/buckets/", s.prefix)
}

func (s *EtcdStore) CreateBucket(ctx context.Context, name string) error {
	return s.CreateBucketWithEncryption(ctx, name, EncryptionConfig{Enabled: false})
}

func (s *EtcdStore) CreateBucketWithEncryption(ctx context.Context, name string, encryption EncryptionConfig) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	key := s.bucketKey(name)

	// Check if already exists
	resp, err := s.client.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to check bucket: %w", err)
	}
	if len(resp.Kvs) > 0 {
		return fmt.Errorf("%w: %s", ErrBucketExists, name)
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
		return fmt.Errorf("%w: %s", ErrBucketExists, name)
	}

	return nil
}

func (s *EtcdStore) GetBucket(ctx context.Context, name string) (*BucketMetadata, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	resp, err := s.client.Get(ctx, s.bucketKey(name))
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrBucketNotFound, name)
	}

	var bucket BucketMetadata
	if err := json.Unmarshal(resp.Kvs[0].Value, &bucket); err != nil {
		return nil, fmt.Errorf("failed to parse bucket: %w", err)
	}

	return &bucket, nil
}

func (s *EtcdStore) BucketExists(ctx context.Context, name string) bool {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	resp, err := s.client.Get(ctx, s.bucketKey(name))
	if err != nil {
		return false
	}
	return len(resp.Kvs) > 0
}

func (s *EtcdStore) IsBucketEncrypted(ctx context.Context, name string) (bool, error) {
	bucket, err := s.GetBucket(ctx, name)
	if err != nil {
		return false, err
	}
	return bucket.Encryption.Enabled, nil
}

func (s *EtcdStore) GetBucketEncryption(ctx context.Context, name string) (*EncryptionConfig, error) {
	bucket, err := s.GetBucket(ctx, name)
	if err != nil {
		return nil, err
	}
	return &bucket.Encryption, nil
}

func (s *EtcdStore) ListBuckets(ctx context.Context) ([]string, error) {
	ctx, cancel := s.withTimeout(ctx)
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

func (s *EtcdStore) DeleteBucket(ctx context.Context, name string) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	// Check if bucket has objects
	objectsResp, err := s.client.Get(ctx, s.objectsPrefix(name), clientv3.WithPrefix(), clientv3.WithLimit(1))
	if err != nil {
		return fmt.Errorf("failed to check bucket objects: %w", err)
	}
	if len(objectsResp.Kvs) > 0 {
		return fmt.Errorf("%w: %s", ErrBucketNotEmpty, name)
	}

	// Delete bucket
	_, err = s.client.Delete(ctx, s.bucketKey(name))
	if err != nil {
		return fmt.Errorf("failed to delete bucket: %w", err)
	}

	return nil
}

func (s *EtcdStore) SaveObjectMetadata(ctx context.Context, meta *ObjectMetadata) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal object: %w", err)
	}

	resp, err := s.client.Put(ctx, s.objectKey(meta.Bucket, meta.Key), string(data))
	if err != nil {
		return fmt.Errorf("failed to save object: %w", err)
	}
	if resp.Header != nil {
		meta.Revision = resp.Header.Revision
	}
	return nil
}

// CompareAndSaveObjectMetadata commits only if the key is still at
// expectedRevision.
//
// etcd does the comparison server-side inside a transaction, so this is safe
// across processes - unlike the local store, where the same guarantee only
// holds because one process owns the directory. This is the reason to run etcd
// once more than one thing writes metadata.
func (s *EtcdStore) CompareAndSaveObjectMetadata(ctx context.Context, meta *ObjectMetadata, expectedRevision int64) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal object: %w", err)
	}

	key := s.objectKey(meta.Bucket, meta.Key)

	// Revision 0 means the caller believes the object does not exist, which in
	// etcd is a key whose version is 0.
	condition := clientv3.Compare(clientv3.ModRevision(key), "=", expectedRevision)
	if expectedRevision == 0 {
		condition = clientv3.Compare(clientv3.Version(key), "=", 0)
	}

	resp, err := s.client.Txn(ctx).
		If(condition).
		Then(clientv3.OpPut(key, string(data))).
		Commit()
	if err != nil {
		return fmt.Errorf("failed to save object: %w", err)
	}
	if !resp.Succeeded {
		return fmt.Errorf("%w: %s/%s changed since it was read (expected revision %d)",
			ErrRevisionMismatch, meta.Bucket, meta.Key, expectedRevision)
	}

	if resp.Header != nil {
		meta.Revision = resp.Header.Revision
	}
	return nil
}

func (s *EtcdStore) GetObjectMetadata(ctx context.Context, bucket, key string) (*ObjectMetadata, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	resp, err := s.client.Get(ctx, s.objectKey(bucket, key))
	if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("%w: %s/%s", ErrObjectNotFound, bucket, key)
	}

	var meta ObjectMetadata
	if err := json.Unmarshal(resp.Kvs[0].Value, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse object: %w", err)
	}

	// etcd's own revision for the key is what a later conditional write
	// compares against, so it replaces anything stored in the value.
	meta.Revision = resp.Kvs[0].ModRevision
	return &meta, nil
}

func (s *EtcdStore) DeleteObjectMetadata(ctx context.Context, bucket, key string) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	resp, err := s.client.Delete(ctx, s.objectKey(bucket, key))
	if err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}
	if resp.Deleted == 0 {
		return fmt.Errorf("%w: %s/%s", ErrObjectNotFound, bucket, key)
	}

	return nil
}

// ListObjects returns one page of a bucket's keys using a bounded range scan.
//
// Both the prefix and the resume point are pushed into the scan, so etcd
// returns at most one page regardless of how many objects the bucket holds.
func (s *EtcdStore) ListObjects(ctx context.Context, bucket string, opts ListOptions) (ListResult, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	if !s.BucketExists(ctx, bucket) {
		return ListResult{}, fmt.Errorf("%w: %s", ErrBucketNotFound, bucket)
	}

	bucketPrefix := s.objectsPrefix(bucket)
	limit := opts.limit()

	// Start at the prefix, or just past the last key of the previous page.
	start := bucketPrefix + opts.Prefix
	if opts.After != "" {
		start = clientv3PrefixSuccessor(bucketPrefix + opts.After)
	}

	// One extra key, so the scan itself reveals whether more remain.
	resp, err := s.client.Get(ctx, start,
		clientv3.WithRange(clientv3.GetPrefixRangeEnd(bucketPrefix+opts.Prefix)),
		clientv3.WithLimit(int64(limit)+1),
		clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend),
	)
	if err != nil {
		return ListResult{}, fmt.Errorf("failed to list objects: %w", err)
	}

	keys := make([]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		keys = append(keys, strings.TrimPrefix(string(kv.Key), bucketPrefix))
	}

	return paginate(keys, limit), nil
}

// clientv3PrefixSuccessor returns the smallest key greater than k, so a scan
// starting there excludes k itself.
func clientv3PrefixSuccessor(k string) string {
	return k + "\x00"
}

var _ MetadataStore = (*EtcdStore)(nil)
