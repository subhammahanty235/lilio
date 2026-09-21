package metadata

import (
	"context"
	"time"
)

/*
Pluggable Metadata Store Interface
===================================

Supports multiple backends:
- Local (JSON files) - Default, simple
- etcd - Distributed, production-grade
- Consul - Alternative distributed KV
- Memory - For testing

Usage:
  store, _ := metadata.NewStore(metadata.Config{
      Type: "local",
      LocalConfig: &metadata.LocalConfig{Path: "./data"},
  })

  store, _ := metadata.NewStore(metadata.Config{
      Type: "etcd",
      EtcdConfig: &metadata.EtcdConfig{Endpoints: []string{"localhost:2379"}},
  })
*/

// MetadataStore keeps the map from object keys to the chunks that make up an
// object. It is the only unreplicated thing in Lilio: chunks live on N nodes
// and are repaired when they go missing, while the map describing them has one
// copy. Losing it turns every chunk into unreadable bytes.
//
// Every method takes a context. The store can be etcd across a network, where
// a call can hang rather than fail, and a request that the client has already
// abandoned should not keep a connection busy.
type MetadataStore interface {
	// Bucket operations
	CreateBucket(ctx context.Context, name string) error
	CreateBucketWithEncryption(ctx context.Context, name string, encryption EncryptionConfig) error
	GetBucket(ctx context.Context, name string) (*BucketMetadata, error)
	ListBuckets(ctx context.Context) ([]string, error)
	DeleteBucket(ctx context.Context, name string) error
	BucketExists(ctx context.Context, name string) bool
	IsBucketEncrypted(ctx context.Context, name string) (bool, error)
	GetBucketEncryption(ctx context.Context, name string) (*EncryptionConfig, error)

	// Object operations
	SaveObjectMetadata(ctx context.Context, meta *ObjectMetadata) error

	// CompareAndSaveObjectMetadata writes metadata only if the stored object is
	// still at expectedRevision, and returns ErrRevisionMismatch otherwise. An
	// expectedRevision of 0 means the object must not already exist.
	//
	// This is what stops two concurrent writes to one key from silently
	// discarding each other. Without it both writers commit, the later one
	// wins, and the earlier one's chunks are left referenced by nothing with
	// neither client told anything went wrong.
	CompareAndSaveObjectMetadata(ctx context.Context, meta *ObjectMetadata, expectedRevision int64) error

	GetObjectMetadata(ctx context.Context, bucket, key string) (*ObjectMetadata, error)
	DeleteObjectMetadata(ctx context.Context, bucket, key string) error

	// ListObjects returns one page of a bucket's keys, in ascending order.
	ListObjects(ctx context.Context, bucket string, opts ListOptions) (ListResult, error)

	// Health & Lifecycle
	Health(ctx context.Context) error
	Close() error
	Type() string
}

// DefaultListLimit bounds a page when the caller does not choose one, so that
// a listing of a large bucket cannot pull the whole thing into memory by
// accident.
const DefaultListLimit = 1000

// MaxListLimit caps what a caller can ask for in one page.
const MaxListLimit = 10000

// ListOptions bounds one page of a listing.
type ListOptions struct {
	// Prefix restricts the listing to keys starting with it.
	Prefix string

	// After resumes the listing from the key following it, exclusive. Pass the
	// NextAfter from the previous page.
	After string

	// Limit is the maximum number of keys to return. 0 means DefaultListLimit.
	Limit int
}

func (o ListOptions) limit() int {
	switch {
	case o.Limit <= 0:
		return DefaultListLimit
	case o.Limit > MaxListLimit:
		return MaxListLimit
	default:
		return o.Limit
	}
}

// ListResult is one page of keys.
type ListResult struct {
	Keys []string

	// NextAfter is the value to pass as ListOptions.After to get the next page.
	// Empty when the listing is complete.
	NextAfter string

	// Truncated reports whether more keys remain.
	Truncated bool
}

type EncryptionConfig struct {
	Enabled   bool   `json:"enabled"`
	Algorithm string `json:"algorithm"`
	Salt      []byte `json:"salt,omitempty"`
	KeyHash   string `json:"key_hash,omitempty"`
}

type BucketMetadata struct {
	Name       string           `json:"name"`
	CreatedAt  time.Time        `json:"created_at"`
	Encryption EncryptionConfig `json:"encryption"`
}

type ChunkInfo struct {
	ChunkID    string `json:"chunk_id"`
	ChunkIndex int    `json:"chunk_index"`
	Size       int64  `json:"size"`
	Checksum   string `json:"checksum"`

	// StorageNodes is where this chunk *belongs*: the full replica set chosen
	// from the hash ring at write time, including any node that was down and
	// did not receive it.
	//
	// It deliberately records intent rather than outcome. If it only listed the
	// nodes that acknowledged the write, then a chunk that reached two of three
	// replicas would look complete - there would be no expectation for reality
	// to fall short of, and under-replication would be undetectable. Storing
	// intent is what gives the scrubber something to compare against.
	//
	// Which of these nodes actually holds the chunk right now is not stored,
	// because it would be stale the moment a disk failed. The scrubber asks.
	StorageNodes []string `json:"storage_nodes"`
}

type ObjectMetadata struct {
	// Revision is assigned by the metadata store and is opaque to callers.
	// Pass the value read from the store back to
	// CompareAndSaveObjectMetadata to make a write conditional on nothing else
	// having changed the object in the meantime. Zero means the object does
	// not exist yet.
	Revision int64 `json:"revision,omitempty"`

	ObjectID    string      `json:"object_id"`
	Bucket      string      `json:"bucket"`
	Key         string      `json:"key"`
	Size        int64       `json:"size"`
	Checksum    string      `json:"checksum"`
	ChunkSize   int         `json:"chunk_size"`
	TotalChunks int         `json:"total_chunks"`
	Chunks      []ChunkInfo `json:"chunks"`
	CreatedAt   time.Time   `json:"created_at"`
	ContentType string      `json:"content_type,omitempty"`
	Encrypted   bool        `json:"encrypted"`
}

type StoreType string

const (
	StoreTypeLocal  StoreType = "local"
	StoreTypeEtcd   StoreType = "etcd"
	StoreTypeConsul StoreType = "consul"
	StoreTypeMemory StoreType = "memory"
)

type Config struct {
	Type         StoreType     `json:"type"`
	LocalConfig  *LocalConfig  `json:"local,omitempty"`
	EtcdConfig   *EtcdConfig   `json:"etcd,omitempty"`
	ConsulConfig *ConsulConfig `json:"consul,omitempty"`
}

type LocalConfig struct {
	Path string `json:"path"`
}

type EtcdConfig struct {
	Endpoints   []string      `json:"endpoints"`
	DialTimeout time.Duration `json:"dial_timeout"`
	Username    string        `json:"username,omitempty"`
	Password    string        `json:"password,omitempty"`
	Prefix      string        `json:"prefix"` // Key prefix, default: "/lilio"
}

type ConsulConfig struct {
	Address string `json:"address"`
	Token   string `json:"token,omitempty"`
	Prefix  string `json:"prefix"` // Key prefix, default: "lilio/"
}
