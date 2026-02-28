package metadata

import "time"

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

type MetadataStore interface {
	// Bucket operations
	CreateBucket(name string) error
	CreateBucketWithEncryption(name string, encryption EncryptionConfig) error
	GetBucket(name string) (*BucketMetadata, error)
	ListBuckets() ([]string, error)
	DeleteBucket(name string) error
	BucketExists(name string) bool
	IsBucketEncrypted(name string) (bool, error)
	GetBucketEncryption(name string) (*EncryptionConfig, error)

	// Object operations
	SaveObjectMetadata(meta *ObjectMetadata) error
	GetObjectMetadata(bucket, key string) (*ObjectMetadata, error)
	DeleteObjectMetadata(bucket, key string) error
	ListObjects(bucket, prefix string) ([]string, error)

	// Health & Lifecycle
	Health() error
	Close() error
	Type() string
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
