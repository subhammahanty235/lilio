package storage

import (
	"context"
	"time"
)

type BackendType string

const (
	BackendTypeLocal   BackendType = "local"
	BackendTypeGDrive  BackendType = "gdrive"
	BackendTypeDropbox BackendType = "dropbox"
	BackendTypeS3      BackendType = "s3"
	BackendTypeSFTP    BackendType = "sftp"
	BackendTypeRemote  BackendType = "remote"
)

type BackendStatus string

const (
	StatusOnline   BackendStatus = "online"
	StatusOffline  BackendStatus = "offline"
	StatusDegraded BackendStatus = "degraded"
)

// statitcs about the storage backends
type BackendStats struct {
	BytesUsed    int64     `json:"bytes_used"`
	BytesFree    int64     `json:"bytes_free"`
	ChunksStored int64     `json:"chunks_stored"`
	LastChecked  time.Time `json:"last_checked"`
}

// Metadata for backends, we can confgure each instance or pod
type BackendInfo struct {
	Name     string        `json:"name"`
	Type     BackendType   `json:"type"`
	Status   BackendStatus `json:"status"`
	Priority int           `json:"priority"` // Lower = preferred
	Stats    BackendStats  `json:"stats"`
}

// StorageBackend is a content-addressed store for chunks. Implementations may
// be a directory on this machine, a remote lilio-chunkd, or a cloud provider.
//
// Every method that can touch storage takes a context. This matters most for
// backends reached over a network: unlike a local file operation, which either
// completes or fails at once, a remote call can accept the connection and then
// never answer. Without a deadline one unresponsive backend would block the
// caller indefinitely - and since writes fan out to N replicas and wait for all
// of them, that means blocking the whole request. The context is what bounds
// that wait, and what lets a disconnecting client cancel the work it started.
//
// Info is the exception: it reports cached state and must not perform I/O, so
// that listing or sorting backends never blocks on a slow one.
type StorageBackend interface {
	// Info returns metadata about this backend. Must not perform I/O.
	Info() BackendInfo

	// Health checks if the backend is accessible
	Health(ctx context.Context) error

	// StoreChunk stores a chunk of data
	StoreChunk(ctx context.Context, chunkID string, data []byte) error

	// RetrieveChunk retrieves a chunk of data
	RetrieveChunk(ctx context.Context, chunkID string) ([]byte, error)

	// DeleteChunk deletes a chunk
	DeleteChunk(ctx context.Context, chunkID string) error

	// HasChunk checks if a chunk exists
	HasChunk(ctx context.Context, chunkID string) bool

	// ListChunks returns all chunk IDs stored in this backend
	ListChunks(ctx context.Context) ([]string, error)

	// Stats returns storage statistics
	Stats(ctx context.Context) (BackendStats, error)
}

type BackendConfig struct {
	Name     string            `yaml:"name" json:"name"`
	Type     BackendType       `yaml:"type" json:"type"`
	Priority int               `yaml:"priority" json:"priority"`
	Options  map[string]string `yaml:"options" json:"options"`
}
