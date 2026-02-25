package storage

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/subhammahanty235/lilio/pkg/crypto"
	"github.com/subhammahanty235/lilio/pkg/hashing"
	"github.com/subhammahanty235/lilio/pkg/metadata"
)

type Lilio struct {
	BasePath          string
	ChunkSize         int
	ReplicationFactor int

	// StorageNodes map[string]*StorageNode
	Registry *Registry
	Metadata *metadata.MetadataStore
	HashRing *hashing.HashRing

	encryptors map[string]*crypto.Encryptor
	encMu      sync.RWMutex

	mu sync.RWMutex
}

type Config struct {
	BasePath          string
	ChunkSize         int
	ReplicationFactor int
}

func DefaultConig() Config {
	return Config{
		BasePath:          "./lilio_data",
		ChunkSize:         1024 * 1024,
		ReplicationFactor: 2,
	}
}

func NewLilioInstance(cfg Config) (*Lilio, error) {
	// metadata store
	metadataStore, err := metadata.NewMetadataStore(cfg.BasePath)
	if err != nil {
		return nil, fmt.Errorf("Failed to create metadata store: %w", err)
	}

	registry := NewRegistry()
	hashRing := hashing.NewHashRing(150)
	obj := &Lilio{
		BasePath:          cfg.BasePath,
		ChunkSize:         cfg.ChunkSize,
		ReplicationFactor: cfg.ReplicationFactor,
		Registry:          registry,
		Metadata:          metadataStore,
		HashRing:          hashRing,
		encryptors:        make(map[string]*crypto.Encryptor),
	}

	fmt.Printf("Lilio initialized:\n")
	fmt.Printf("  - Chunk size: %d KB\n", cfg.ChunkSize/1024)
	fmt.Printf("  - Replication factor: %d\n", cfg.ReplicationFactor)
	fmt.Printf("  - Using: Consistent Hashing\n")
	return obj, nil
}

func (s *Lilio) AddBackend(backend StorageBackend) error {
	err := s.Registry.Add(backend)
	if err != nil {
		return err
	}

	s.HashRing.AddNode(backend.Info().Name)
	return nil
}

// RemoveBackend removes a storage backend
func (s *Lilio) RemoveBackend(name string) error {
	err := s.Registry.Remove(name)
	if err != nil {
		return err
	}

	s.HashRing.RemoveNode(name)
	return nil
}

// ListBackends returns info about all backends
func (s *Lilio) ListBackends() []BackendInfo {
	var infos []BackendInfo
	for _, backend := range s.Registry.List() {
		infos = append(infos, backend.Info())
	}
	return infos
}

func (s *Lilio) ChunkData(data []byte) [][]byte {
	var chunks [][]byte
	for i := 0; i < len(data); i += s.ChunkSize {
		end := min(i+s.ChunkSize, len(data))
		chunks = append(chunks, data[i:end])
	}

	return chunks
}

func (s *Lilio) SelectNodesForChunk(chunkId string) []StorageBackend {
	// Get sorted node IDs for consistent selection
	if s.HashRing.IsEmpty() {
		return nil
	}
	nodeNames := s.HashRing.GetNodes(chunkId, s.ReplicationFactor)

	if len(nodeNames) == 0 {
		return nil
	}

	var selected []StorageBackend
	for _, name := range nodeNames {
		backend, err := s.Registry.Get(name)
		if err == nil && backend.Info().Status == StatusOnline {
			selected = append(selected, backend)
		}
	}

	return selected
}

func (s *Lilio) CreateBucketWithEncryption(bucketName, password string) error {
	salt, err := crypto.GenerateSalt()
	if err != nil {
		return fmt.Errorf("failed to generate salt: %w", err)
	}

	enc, err := crypto.NewEncryptorFromPassword(password, salt)
	if err != nil {
		return fmt.Errorf("failed to create encryptor: %w", err)
	}

	s.encMu.Lock()
	s.encryptors[bucketName] = enc
	s.encMu.Unlock()

	keyHash := sha256.Sum256([]byte(password))

	encConfig := metadata.EncryptionConfig{
		Enabled:   true,
		Algorithm: "aes256-gcm",
		Salt:      salt,
		KeyHash:   hex.EncodeToString(keyHash[:]),
	}

	return s.Metadata.CreateBucketWithEncryption(bucketName, encConfig)
}

// Public API
// Craete bucket
func (s *Lilio) CreateBucket(bucketname string) error {
	return s.Metadata.CreateBucket(bucketname)
}

func (s *Lilio) UnlockBucket(bucketName, password string) error {
	encConfig, err := s.Metadata.GetBucketEncryption(bucketName)
	if err != nil {
		return err
	}

	if !encConfig.Enabled {
		return fmt.Errorf("bucket is not encrypted")
	}

	keyHash := sha256.Sum256([]byte(password))
	if hex.EncodeToString(keyHash[:]) != encConfig.KeyHash {
		return fmt.Errorf("invalid password")
	}

	enc, err := crypto.NewEncryptorFromPassword(password, encConfig.Salt)
	if err != nil {
		return fmt.Errorf("failed to create encryptor: %w", err)
	}

	s.encMu.Lock()
	s.encryptors[bucketName] = enc
	s.encMu.Unlock()

	return nil
}

func (s *Lilio) IsBucketUnlocked(bucketName string) bool {
	s.encMu.RLock()
	_, exists := s.encryptors[bucketName]
	s.encMu.RUnlock()
	return exists
}
func (s *Lilio) getEncryptor(bucketName string) *crypto.Encryptor {
	s.encMu.RLock()
	defer s.encMu.RUnlock()
	return s.encryptors[bucketName]
}

// List buckets
func (s *Lilio) ListBuckets() ([]string, error) {
	return s.Metadata.ListBuckets()
}

// Todo : Delete buckets

// Put object

// func (s *Lilio) PutObjectOld(bucket, key string, data []byte, contentType string) (*metadata.ObjectMetadata, error) {

// 	// TODO : Cond --> check if bucket exists

// 	objectId := generateUUID()
// 	chunks := s.ChunkData(data)
// 	totalChunks := len(chunks)

// 	fmt.Printf("\nPutting object: %s/%s\n", bucket, key)
// 	fmt.Printf("  - Size: %d bytes\n", len(data))
// 	fmt.Printf("  - Chunks: %d\n", totalChunks)

// 	var chunkInfos []metadata.ChunkInfo
// 	for i, chunkData := range chunks {
// 		chunkId := fmt.Sprintf("%s_chunk_%d", objectId, i)

// 		chunkCheckSum := CalculateChecksum(chunkData)
// 		targetNodes := s.SelectNodesForChunk(i)
// 		if len(targetNodes) == 0 {
// 			return nil, fmt.Errorf("no healthy backends available")
// 		}

// 		var successfulNodes []string
// 		var wg sync.WaitGroup
// 		var mu sync.Mutex

// 		for _, nodeId := range targetNodes {
// 			wg.Add(1)
// 			go func(b StorageBackend) {
// 				defer wg.Done()

// 				if err := b.StoreChunk(chunkId, chunkData); err == nil {
// 					mu.Lock()
// 					successfulNodes = append(successfulNodes, b.Info().Name)
// 					mu.Unlock()
// 				}
// 			}(nodeId)
// 		}

// 		wg.Wait()
// 		if len(successfulNodes) == 0 {
// 			return nil, fmt.Errorf("failed to store chunk %d", i)
// 		}

// 		sort.Strings(successfulNodes)

// 		chunkInfo := metadata.ChunkInfo{
// 			ChunkID:      chunkId,
// 			ChunkIndex:   i,
// 			Size:         int64(len(chunkData)),
// 			Checksum:     chunkCheckSum,
// 			StorageNodes: successfulNodes,
// 		}

// 		chunkInfos = append(chunkInfos, chunkInfo)
// 		fmt.Printf("Chunk %d: stored on %v\n", i, successfulNodes)
// 	}

// 	meta := &metadata.ObjectMetadata{
// 		ObjectID:    objectId,
// 		Bucket:      bucket,
// 		Key:         key,
// 		Size:        int64(len(data)),
// 		Checksum:    CalculateChecksum(data),
// 		ChunkSize:   s.ChunkSize,
// 		TotalChunks: totalChunks,
// 		Chunks:      chunkInfos,
// 		CreatedAt:   time.Now().UTC(),
// 		ContentType: contentType,
// 	}

// 	if err := s.Metadata.SaveObjectMetadata(meta); err != nil {
// 		return nil, fmt.Errorf("failed to save metadata: %w", err)
// 	}

// 	fmt.Println("Object Stored successfully")
// 	return meta, nil
// }

func (s *Lilio) PutObject(bucket, key string, data []byte, contentType string) (*metadata.ObjectMetadata, error) {
	if !s.Metadata.BucketExists(bucket) {
		return nil, fmt.Errorf("bucket does not exist: %s", bucket)
	}

	isEncrypted, _ := s.Metadata.IsBucketEncrypted(bucket)
	var encryptor *crypto.Encryptor

	if isEncrypted {
		encryptor = s.getEncryptor(bucket)
		if encryptor == nil {
			return nil, fmt.Errorf("bucket is encrypted but not unlocked. Use: lilio bucket unlock %s", bucket)
		}
	}

	objectId := generateUUID()
	originalSize := len(data)

	if encryptor != nil {
		var err error
		data, err = encryptor.Encrypt(data)
		if err != nil {
			return nil, fmt.Errorf("encryption failed: %w", err)
		}
		fmt.Printf("  🔐 Data encrypted (%d → %d bytes)\n", originalSize, len(data))
	}

	chunks := s.ChunkData(data)
	totalChunks := len(chunks)

	fmt.Printf("\nPutting object: %s/%s\n", bucket, key)
	fmt.Printf("  - Original Size: %d bytes\n", originalSize)
	if isEncrypted {
		fmt.Printf("  - Encrypted Size: %d bytes\n", len(data))
	}
	fmt.Printf("  - Chunks: %d\n", totalChunks)

	var chunkInfos []metadata.ChunkInfo
	for i, chunkData := range chunks {
		chunkId := fmt.Sprintf("%s_chunk_%d", objectId, i)
		chunkCheckSum := CalculateChecksum(chunkData)

		targetNodes := s.SelectNodesForChunk(chunkId)
		if len(targetNodes) == 0 {
			return nil, fmt.Errorf("no healthy backends available")
		}

		var successfulNodes []string
		var wg sync.WaitGroup
		var mu sync.Mutex

		for _, node := range targetNodes {
			wg.Add(1)
			go func(b StorageBackend) {
				defer wg.Done()
				if err := b.StoreChunk(chunkId, chunkData); err == nil {
					mu.Lock()
					successfulNodes = append(successfulNodes, b.Info().Name)
					mu.Unlock()
				}
			}(node)
		}

		wg.Wait()
		if len(successfulNodes) == 0 {
			return nil, fmt.Errorf("failed to store chunk %d", i)
		}

		sort.Strings(successfulNodes)

		chunkInfo := metadata.ChunkInfo{
			ChunkID:      chunkId,
			ChunkIndex:   i,
			Size:         int64(len(chunkData)),
			Checksum:     chunkCheckSum,
			StorageNodes: successfulNodes,
		}

		chunkInfos = append(chunkInfos, chunkInfo)
		fmt.Printf("  ✓ Chunk %d: stored on %v\n", i, successfulNodes)
	}

	meta := &metadata.ObjectMetadata{
		ObjectID:    objectId,
		Bucket:      bucket,
		Key:         key,
		Size:        int64(originalSize),
		Checksum:    CalculateChecksum(data),
		ChunkSize:   s.ChunkSize,
		TotalChunks: totalChunks,
		Chunks:      chunkInfos,
		CreatedAt:   time.Now().UTC(),
		ContentType: contentType,
		Encrypted:   isEncrypted,
	}

	if err := s.Metadata.SaveObjectMetadata(meta); err != nil {
		return nil, fmt.Errorf("failed to save metadata: %w", err)
	}

	fmt.Println("  ✓ Object stored successfully!")
	return meta, nil
}

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (s *Lilio) GetObjectOld(bucket, key string) ([]byte, error) {

	//  --------- flow ------------
	// Fetch the metadata

	// Short the chunks by index

	// retrieve chunks
	//     --> try each node that has this chunk
	//          --> verify checksum

	// deassemble the data
	// ----------------------------

	meta, err := s.Metadata.GetObjectMetadata(bucket, key)
	if err != nil {
		return nil, err
	}

	fmt.Printf("\nGetting object: %s/%s\n", bucket, key)
	fmt.Printf("  - Size: %d bytes\n", meta.Size)
	fmt.Printf("  - Chunks: %d\n", meta.TotalChunks)

	sort.Slice(meta.Chunks, func(i, j int) bool {
		return meta.Chunks[i].ChunkIndex < meta.Chunks[j].ChunkIndex
	})

	var chunksData [][]byte
	for _, chunkInfo := range meta.Chunks {
		var chunkData []byte
		var retrieved bool

		// try each node that has this chunk

		for _, nodeId := range chunkInfo.StorageNodes {
			backend, err := s.Registry.Get(nodeId)
			if err != nil {
				fmt.Printf("  ⚠ Chunk %d: %s unavailable, trying next...\n", chunkInfo.ChunkIndex, nodeId)
				continue
			}

			data, err := backend.RetrieveChunk(chunkInfo.ChunkID)
			if err != nil {
				continue
			}

			if CalculateChecksum(data) == chunkInfo.Checksum {
				chunkData = data
				retrieved = true
				fmt.Printf("  ✓ Chunk %d: retrieved from %s\n", chunkInfo.ChunkIndex, nodeId)
				break
			} else {
				fmt.Printf("  ⚠ Chunk %d: checksum mismatch on %s\n", chunkInfo.ChunkIndex, nodeId)

			}
		}
		if !retrieved {
			return nil, fmt.Errorf("failed to retrieve chunk %d", chunkInfo.ChunkIndex)
		}

		chunksData = append(chunksData, chunkData)
	}

	fullData := bytes.Join(chunksData, nil)
	if CalculateChecksum(fullData) != meta.Checksum {
		return nil, fmt.Errorf("final checksum verification failed")
	}

	fmt.Printf("  ✓ Object retrieved successfully!\n")

	return fullData, nil
}

func (s *Lilio) GetObject(bucket, key string) ([]byte, error) {
	meta, err := s.Metadata.GetObjectMetadata(bucket, key)
	if err != nil {
		return nil, err
	}

	var encryptor *crypto.Encryptor
	if meta.Encrypted {
		encryptor = s.getEncryptor(bucket)
		if encryptor == nil {
			return nil, fmt.Errorf("object is encrypted but bucket not unlocked. Use: lilio bucket unlock %s", bucket)
		}
	}

	fmt.Printf("\nGetting object: %s/%s\n", bucket, key)
	fmt.Printf("  - Size: %d bytes\n", meta.Size)
	fmt.Printf("  - Chunks: %d\n", meta.TotalChunks)
	if meta.Encrypted {
		fmt.Printf("  - 🔐 Encrypted: Yes\n")
	}

	sort.Slice(meta.Chunks, func(i, j int) bool {
		return meta.Chunks[i].ChunkIndex < meta.Chunks[j].ChunkIndex
	})

	var chunksData [][]byte
	for _, chunkInfo := range meta.Chunks {
		var chunkData []byte
		var retrieved bool

		for _, nodeName := range chunkInfo.StorageNodes {
			backend, err := s.Registry.Get(nodeName)
			if err != nil {
				fmt.Printf("  ⚠ Chunk %d: %s unavailable, trying next...\n", chunkInfo.ChunkIndex, nodeName)
				continue
			}

			data, err := backend.RetrieveChunk(chunkInfo.ChunkID)
			if err != nil {
				continue
			}

			if CalculateChecksum(data) == chunkInfo.Checksum {
				chunkData = data
				retrieved = true
				fmt.Printf("  ✓ Chunk %d: retrieved from %s\n", chunkInfo.ChunkIndex, nodeName)
				break
			} else {
				fmt.Printf("  ⚠ Chunk %d: checksum mismatch on %s\n", chunkInfo.ChunkIndex, nodeName)
			}
		}

		if !retrieved {
			return nil, fmt.Errorf("failed to retrieve chunk %d", chunkInfo.ChunkIndex)
		}

		chunksData = append(chunksData, chunkData)
	}

	fullData := bytes.Join(chunksData, nil)

	if CalculateChecksum(fullData) != meta.Checksum {
		return nil, fmt.Errorf("final checksum verification failed")
	}

	if encryptor != nil {
		fullData, err = encryptor.Decrypt(fullData)
		if err != nil {
			return nil, fmt.Errorf("decryption failed: %w", err)
		}
		fmt.Printf("  🔓 Data decrypted\n")
	}

	fmt.Printf("  ✓ Object retrieved successfully!\n")
	return fullData, nil
}

func (s *Lilio) HeadObject(bucket, key string) (*metadata.ObjectMetadata, error) {
	return s.Metadata.GetObjectMetadata(bucket, key)
}

func (s *Lilio) DeleteObject(bucket, key string) error {
	meta, err := s.Metadata.GetObjectMetadata(bucket, key)
	if err != nil {
		return err
	}

	// Delete all chunks
	for _, chunkInfo := range meta.Chunks {
		for _, backendName := range chunkInfo.StorageNodes {
			backend, err := s.Registry.Get(backendName)
			if err == nil {
				backend.DeleteChunk(chunkInfo.ChunkID)
			}
		}
	}

	return s.Metadata.DeleteObjectMetadata(bucket, key)
}

func (s *Lilio) ListObjects(bucket, prefix string) ([]string, error) {
	return s.Metadata.ListObjects(bucket, prefix)
}

// Storage stats
func (s *Lilio) GetStorageStats() map[string]map[string]interface{} {
	stats := make(map[string]map[string]interface{})

	for _, backend := range s.Registry.List() {
		info := backend.Info()
		backendStats, _ := backend.Stats()

		stats[info.Name] = map[string]interface{}{
			"node_id":       info.Name,
			"path":          info.Type,
			"status":        info.Status,
			"chunks_stored": backendStats.ChunksStored,
			"bytes_stored":  backendStats.BytesUsed,
		}
	}

	return stats
}

func (s *Lilio) HealthCheck() map[string]error {
	return s.Registry.HealthCheck()
}
