package storage

import "time"

type BackendType string

const (
	BackendTypeLocal   BackendType = "local"
	BackendTypeGDrive  BackendType = "gdrive"
	BackendTypeDropbox BackendType = "dropbox"
	BackendTypeS3      BackendType = "s3"
	BackendTypeSFTP    BackendType = "sftp"
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

type StorageBackend interface {
	// Info returns metadata about this backend
	Info() BackendInfo

	// Health checks if the backend is accessible
	Health() error

	// StoreChunk stores a chunk of data
	StoreChunk(chunkID string, data []byte) error

	// RetrieveChunk retrieves a chunk of data
	RetrieveChunk(chunkID string) ([]byte, error)

	// DeleteChunk deletes a chunk
	DeleteChunk(chunkID string) error

	// HasChunk checks if a chunk exists
	HasChunk(chunkID string) bool

	// ListChunks returns all chunk IDs stored in this backend
	ListChunks() ([]string, error)

	// Stats returns storage statistics
	Stats() (BackendStats, error)
}

type BackendConfig struct {
	Name     string            `yaml:"name" json:"name"`
	Type     BackendType       `yaml:"type" json:"type"`
	Priority int               `yaml:"priority" json:"priority"`
	Options  map[string]string `yaml:"options" json:"options"`
}
