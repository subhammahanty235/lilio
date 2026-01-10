package storage

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type StorageNode struct {
	NodeId   string
	BasePath string
	mu       sync.RWMutex

	ChunkStored int64
	BytesStored int64
}

func NewStorageNode(nodeId, basePath string) (*StorageNode, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage path %w", err)
	}
	return &StorageNode{
		NodeId:   nodeId,
		BasePath: basePath,
	}, nil
}

func (s *StorageNode) StoreChunk(chunkId string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	chunkPath := filepath.Join(s.BasePath, chunkId)

	if err := os.WriteFile(chunkPath, data, 0644); err != nil {
		return fmt.Errorf("failed to store chunk %s : %w", chunkId, err)
	}

	s.ChunkStored++
	s.BytesStored += int64(len(data))

	return nil
}

func (s *StorageNode) RetrieveChunk(chunkId string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	chunkPath := filepath.Join(s.BasePath, chunkId)
	data, err := os.ReadFile(chunkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Chunk not found: %s", chunkId)
		}
		return nil, fmt.Errorf("Failed to read chunk %s : %w", chunkId, err)
	}
	return data, nil
}

func CalculateChecksum(data []byte) string {
	hash := md5.Sum(data)
	return hex.EncodeToString(hash[:])
}
