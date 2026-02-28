package storagemodels

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/subhammahanty235/lilio/pkg/storage"
	// "github.com/subhammahanty235/lilio/pkg/storage"
)

type LocalBackendPod struct {
	name     string
	basePath string
	priority int
	mu       sync.RWMutex

	chunksStored int64
	bytesStored  int64
}

func NewLocalBackendPod(name, basePath string, priority int) (*LocalBackendPod, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage path: %w", err)
	}
	backend := &LocalBackendPod{
		name:     name,
		basePath: basePath,
		priority: priority,
	}

	chunks, _ := backend.ListChunks()
	backend.chunksStored = int64(len(chunks))
	return backend, nil
}

// function to return the metadata about this backend

func (l *LocalBackendPod) Info() storage.BackendInfo {
	stats, _ := l.Stats()
	status := storage.StatusOnline
	if err := l.Health(); err != nil {
		status = storage.StatusOffline
	}

	return storage.BackendInfo{
		Name:     l.name,
		Type:     storage.BackendTypeLocal,
		Status:   status,
		Priority: l.priority,
		Stats:    stats,
	}
}

func (l *LocalBackendPod) Health() error {
	// Check if directory exists and is writable
	testFile := filepath.Join(l.basePath, ".health_check")

	if err := os.WriteFile(testFile, []byte("ok"), 0644); err != nil {
		return fmt.Errorf("backend not writable: %w", err)
	}

	os.Remove(testFile)
	return nil
}

func (l *LocalBackendPod) ListChunks() ([]string, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	entries, err := os.ReadDir(l.basePath)
	if err != nil {
		return nil, fmt.Errorf("failed to list chunks: %w", err)
	}

	var chunks []string
	for _, entry := range entries {
		if !entry.IsDir() && entry.Name() != ".health_check" {
			chunks = append(chunks, entry.Name())
		}
	}

	return chunks, nil
}

func (l *LocalBackendPod) Stats() (storage.BackendStats, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var stat syscall.Statfs_t
	var bytesFree int64 = -1

	if err := syscall.Statfs(l.basePath, &stat); err == nil {
		bytesFree = int64(stat.Bavail) * int64(stat.Bsize)
	}

	return storage.BackendStats{
		BytesUsed:    l.bytesStored,
		BytesFree:    bytesFree,
		ChunksStored: l.chunksStored,
		LastChecked:  time.Now(),
	}, nil
}

func (l *LocalBackendPod) StoreChunk(chunkID string, data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	chunkPath := filepath.Join(l.basePath, chunkID)

	if err := os.WriteFile(chunkPath, data, 0644); err != nil {
		return fmt.Errorf("failed to store chunk %s: %w", chunkID, err)
	}

	l.chunksStored++
	l.bytesStored += int64(len(data))

	return nil
}

func (l *LocalBackendPod) RetrieveChunk(chunkID string) ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	chunkPath := filepath.Join(l.basePath, chunkID)

	data, err := os.ReadFile(chunkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("chunk not found: %s", chunkID)
		}
		return nil, fmt.Errorf("failed to read chunk %s: %w", chunkID, err)
	}

	return data, nil
}

func (l *LocalBackendPod) HasChunk(chunkID string) bool {
	chunkPath := filepath.Join(l.basePath, chunkID)
	_, err := os.Stat(chunkPath)
	return err == nil
}

func (l *LocalBackendPod) DeleteChunk(chunkID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	chunkPath := filepath.Join(l.basePath, chunkID)

	if err := os.Remove(chunkPath); err != nil {
		if os.IsNotExist(err) {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to delete chunk %s: %w", chunkID, err)
	}

	l.chunksStored--

	return nil
}
