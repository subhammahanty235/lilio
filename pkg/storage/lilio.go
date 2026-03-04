package storage

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/subhammahanty235/lilio/pkg/crypto"
	"github.com/subhammahanty235/lilio/pkg/hashing"
	"github.com/subhammahanty235/lilio/pkg/metadata"
	"github.com/subhammahanty235/lilio/pkg/metrics"
	"github.com/subhammahanty235/lilio/pkg/utils"
)

type QuorumConfig struct {
	N int
	R int
	W int
}

func DefaultQuorum(rf int) QuorumConfig {
	quorum := (rf / 2) + 1
	return QuorumConfig{
		N: rf,
		R: quorum,
		W: quorum,
	}

}

type FailedBackend struct {
	Name     string
	Type     string
	Priority int
	Error    string
}

type Lilio struct {
	BasePath          string
	ChunkSize         int
	ReplicationFactor int

	// StorageNodes map[string]*StorageNode
	Registry       *Registry
	FailedBackends map[string]*FailedBackend // Track backends that failed to initialize
	Metadata       metadata.MetadataStore
	HashRing       *hashing.HashRing

	Metrics    metrics.Collector
	encryptors map[string]*crypto.Encryptor
	encMu      sync.RWMutex
	Quorum     QuorumConfig
	mu         sync.RWMutex
}

type Config struct {
	BasePath          string
	ChunkSize         int
	ReplicationFactor int
	MetadataConfig    *metadata.Config
	MetricsConfig     *metrics.Config
	Quorum            *QuorumConfig //custom quorum settings
}

func DefaultConig() Config {
	return Config{
		BasePath:          "./lilio_data",
		ChunkSize:         1024 * 1024,
		ReplicationFactor: 3,
	}
}

func NewLilioInstance(cfg Config) (*Lilio, error) {
	// metadata store
	var metadataStore metadata.MetadataStore
	var err error

	if cfg.MetadataConfig != nil {
		// Use custom metadata config
		metadataStore, err = metadata.NewStore(*cfg.MetadataConfig)
	} else {
		// Default to local store
		metadataStore, err = metadata.NewStore(metadata.Config{
			Type: metadata.StoreTypeLocal,
			LocalConfig: &metadata.LocalConfig{
				Path: cfg.BasePath + "/metadata",
			},
		})
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create metadata store: %w", err)
	}

	var quorum QuorumConfig
	if cfg.Quorum != nil {
		quorum = *cfg.Quorum
		if quorum.W+quorum.R <= quorum.N {
			return nil, fmt.Errorf("invalid quorum: W(%d) + R(%d) must be > N(%d)", quorum.W, quorum.R, quorum.N)
		}
	} else {
		quorum = DefaultQuorum(cfg.ReplicationFactor)
	}

	var metricsCollector metrics.Collector
	if cfg.MetricsConfig != nil {
		metricsCollector, err = metrics.NewCollector(*cfg.MetricsConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create metrics collector: %w", err)
		}
	} else {
		metricsCollector, _ = metrics.NewCollector(metrics.DefaultConfig())
	}

	registry := NewRegistry()
	hashRing := hashing.NewHashRing(150)
	obj := &Lilio{
		BasePath:          cfg.BasePath,
		ChunkSize:         cfg.ChunkSize,
		ReplicationFactor: cfg.ReplicationFactor,
		Registry:          registry,
		FailedBackends:    make(map[string]*FailedBackend),
		Metadata:          metadataStore,
		HashRing:          hashRing,
		Metrics:           metricsCollector,
		Quorum:            quorum,
		encryptors:        make(map[string]*crypto.Encryptor),
	}

	fmt.Printf("Lilio initialized:\n")
	fmt.Printf("  - Chunk size: %d KB\n", cfg.ChunkSize/1024)
	fmt.Printf("  - Replication: N=%d, W=%d, R=%d\n", quorum.N, quorum.W, quorum.R)
	fmt.Printf("  - Using: Consistent Hashing\n")
	fmt.Printf("  - Metrics: %s\n", metricsCollector.Type())
	return obj, nil
}

func (s *Lilio) AddBackend(backend StorageBackend) error {
	err := s.Registry.Add(backend)
	if err != nil {
		return err
	}

	info := backend.Info()
	s.HashRing.AddNode(info.Name)
	return nil
}

// TrackFailedBackend records a backend that failed to initialize
func (s *Lilio) TrackFailedBackend(name, backendType string, priority int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.FailedBackends[name] = &FailedBackend{
		Name:     name,
		Type:     backendType,
		Priority: priority,
		Error:    err.Error(),
	}
}

// GetFailedBackends returns all backends that failed to initialize
func (s *Lilio) GetFailedBackends() map[string]*FailedBackend {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]*FailedBackend)
	for k, v := range s.FailedBackends {
		result[k] = v
	}
	return result
}

// 	s.HashRing.AddNode(backend.Info().Name)
// 	return nil
// }

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

func (s *Lilio) PutObject(bucket, key string, reader io.Reader, size int64, contentType string) (*metadata.ObjectMetadata, error) {
	if !s.Metadata.BucketExists(bucket) {
		return nil, fmt.Errorf("bucket does not exist: %s", bucket)
	}

	startTime := time.Now()
	isEncrypted, _ := s.Metadata.IsBucketEncrypted(bucket)
	var encryptor *crypto.Encryptor

	if isEncrypted {
		encryptor = s.getEncryptor(bucket)
		if encryptor == nil {
			return nil, fmt.Errorf("bucket is encrypted but not unlocked. Use: lilio bucket unlock %s", bucket)
		}
	}

	objectId := generateUUID()
	fmt.Printf("\nPutting object (streaming): %s/%s\n", bucket, key)
	fmt.Printf("  - Size: %d bytes\n", size)
	if isEncrypted {
		fmt.Printf("  - 🔐 Encryption: Enabled\n")
	}

	chunkReader := utils.NewChunkReader(reader, s.ChunkSize)
	var chunkInfos []metadata.ChunkInfo
	var totalSize int64 = 0
	var fullChecksum = sha256.New()

	for {
		chunkData, chunkIndex, err := chunkReader.NextChunk()
		if err == io.EOF && chunkData == nil {
			break // Done!
		}

		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read chunk %d: %w", chunkIndex, err)
		}

		// Update full file checksum
		fullChecksum.Write(chunkData)
		totalSize += int64(len(chunkData))

		originalChunkSize := len(chunkData)
		if encryptor != nil {
			chunkData, err = encryptor.Encrypt(chunkData)
			if err != nil {
				return nil, fmt.Errorf("encryption failed for chunk %d: %w", chunkIndex, err)
			}
		}
		// Generate chunk ID and checksum
		chunkId := fmt.Sprintf("%s_chunk_%d", objectId, chunkIndex)
		chunkChecksum := CalculateChecksum(chunkData)

		// select the target nodes using consistent hashing
		targetNodes := s.SelectNodesForChunk(chunkId)
		if len(targetNodes) == 0 {
			return nil, fmt.Errorf("no healthy backends available for chunk %d", chunkIndex)
		}

		// Store chunk on multiple nodes (parallel)
		var successfulNodes []string
		var wg sync.WaitGroup
		var mu sync.Mutex

		version := time.Now().UnixNano()

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

		// Record quorum write metrics
		success := len(successfulNodes) >= s.Quorum.W
		s.Metrics.RecordQuorumWrite(success, len(targetNodes), len(successfulNodes))

		if !success {
			return nil, fmt.Errorf("write quorum failed for chunk %d: got %d/%d nodes",
				chunkIndex, len(successfulNodes), s.Quorum.W)
		}

		sort.Strings(successfulNodes)
		chunkInfo := metadata.ChunkInfo{
			ChunkID:      chunkId,
			ChunkIndex:   chunkIndex,
			Size:         int64(originalChunkSize),
			Checksum:     chunkChecksum,
			StorageNodes: successfulNodes,
			Version:      version,
		}
		chunkInfos = append(chunkInfos, chunkInfo)
		fmt.Printf("  ✓ Chunk %d: %d bytes → stored on %v\n", chunkIndex, originalChunkSize, successfulNodes)

		for _, nodename := range successfulNodes {
			s.Metrics.RecordChunkStored(nodename, int64(originalChunkSize))
		}
	}

	meta := &metadata.ObjectMetadata{
		ObjectID:    objectId,
		Bucket:      bucket,
		Key:         key,
		Size:        totalSize,
		Checksum:    hex.EncodeToString(fullChecksum.Sum(nil)),
		ChunkSize:   s.ChunkSize,
		TotalChunks: len(chunkInfos),
		Chunks:      chunkInfos,
		CreatedAt:   time.Now().UTC(),
		ContentType: contentType,
		Encrypted:   isEncrypted,
	}

	if err := s.Metadata.SaveObjectMetadata(meta); err != nil {
		return nil, fmt.Errorf("failed to save metadata: %w", err)
	}
	s.Metrics.RecordPutObject(bucket, totalSize, time.Since(startTime))
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
	startTime := time.Now()
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
	s.Metrics.RecordGetObject(bucket, meta.Size, time.Since(startTime))

	fmt.Printf("  ✓ Object retrieved successfully!\n")

	return fullData, nil
}

func (s *Lilio) GetObject(bucket, key string, writer io.Writer) error {
	meta, err := s.Metadata.GetObjectMetadata(bucket, key)
	if err != nil {
		return err
	}

	var encryptor *crypto.Encryptor
	if meta.Encrypted {
		encryptor = s.getEncryptor(bucket)
		if encryptor == nil {
			return fmt.Errorf("object is encrypted but bucket not unlocked. Use: lilio bucket unlock %s", bucket)
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

	// var chunksData [][]byte
	for _, chunkInfo := range meta.Chunks {
		chunkData, err := s.retrieveChunk(chunkInfo)
		if err != nil {
			return fmt.Errorf("failed to retrieve chunk %d: %w", chunkInfo.ChunkIndex, err)

		}

		if encryptor != nil {
			chunkData, err = encryptor.Decrypt(chunkData)
			if err != nil {
				return fmt.Errorf("decryption failed for chunk %d: %w", chunkInfo.ChunkIndex, err)
			}
		}

		_, err = writer.Write(chunkData)
		if err != nil {
			return fmt.Errorf("failed to write chunk %d: %w", chunkInfo.ChunkIndex, err)
		}

		fmt.Printf("  ✓ Chunk %d: streamed\n", chunkInfo.ChunkIndex)

	}

	fmt.Printf("  ✓ Object streamed successfully!\n")
	return nil
}

type ChunkResponse struct {
	Data     []byte
	Checksum string
	NodeName string
	Valid    bool
}

func (s *Lilio) retrieveChunk(chunkInfo metadata.ChunkInfo) ([]byte, error) {
	var response []ChunkResponse
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, nodeName := range chunkInfo.StorageNodes {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			backend, err := s.Registry.Get(name)
			if err != nil {
				return
			}
			data, err := backend.RetrieveChunk(chunkInfo.ChunkID)
			if err != nil {
				return
			}

			// Record chunk retrieval
			s.Metrics.RecordChunkRetrieved(name, int64(len(data)))

			checksum := CalculateChecksum(data)
			valid := checksum == chunkInfo.Checksum
			mu.Lock()
			response = append(response, ChunkResponse{
				Data:     data,
				Checksum: checksum,
				NodeName: name,
				Valid:    valid,
			})
			mu.Unlock()
		}(nodeName)
	}
	wg.Wait()

	// Record quorum read metrics
	success := len(response) >= s.Quorum.R
	s.Metrics.RecordQuorumRead(success, len(chunkInfo.StorageNodes), len(response))

	if !success {
		return nil, fmt.Errorf("read quorum failed: got %d/%d nodes", len(response), s.Quorum.R)
	}

	var validResponses []ChunkResponse
	var staleNodes []string
	for _, resp := range response {
		if resp.Valid {
			validResponses = append(validResponses, resp)
		} else {
			staleNodes = append(staleNodes, resp.NodeName)
		}
	}
	if len(validResponses) == 0 {
		return nil, fmt.Errorf("no valid chunk data found (all checksums failed)")
	}
	if len(staleNodes) > 0 {
		go s.readRepair(chunkInfo.ChunkID, validResponses[0].Data, staleNodes)
	}
	fmt.Printf("    Quorum R=%d/%d, valid=%d, repaired=%d\n",
		len(response), s.Quorum.R, len(validResponses), len(staleNodes))

	return validResponses[0].Data, nil
}

func (s *Lilio) readRepair(chunkId string, data []byte, staleNodes []string) {
	for _, nodeName := range staleNodes {
		backend, err := s.Registry.Get(nodeName)
		if err != nil {
			continue
		}

		if err := backend.StoreChunk(chunkId, data); err == nil {
			fmt.Printf("    🔧 Read repair: fixed %s on %s\n", chunkId, nodeName)
			s.Metrics.RecordReadRepair(nodeName)
		}
	}
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
				s.Metrics.RecordChunkDeleted(backendName)
			}
		}
	}

	err = s.Metadata.DeleteObjectMetadata(bucket, key)
	if err == nil {
		s.Metrics.RecordDeleteObject(bucket)
	}

	return err
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
	healthStatus := s.Registry.HealthCheck()

	// Record backend health metrics
	for nodeName, err := range healthStatus {
		healthy := err == nil
		s.Metrics.RecordBackendHealth(nodeName, healthy)
	}

	return healthStatus
}
