package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

// QuorumConfig is the replication policy: how many copies of a chunk exist,
// and how many must be written before the write is considered durable.
//
// There is deliberately no read quorum. A read quorum exists to solve one
// problem - deciding which of several differing replicas of a mutable value is
// current - and Lilio does not have that problem. A chunk ID is minted from a
// fresh object ID on every PUT and is never overwritten, so every copy of a
// chunk is either byte-identical to the others or damaged, and the checksum in
// the metadata already says which. Requiring R replicas to agree added nothing
// the checksum did not already guarantee, while making an object unreadable
// whenever fewer than R nodes were reachable - even with an intact, verified
// copy sitting on one of them.
type QuorumConfig struct {
	// N is the replication factor: how many nodes a chunk belongs on.
	N int
	// W is the write quorum: how many of those must accept a chunk before the
	// write is allowed to commit. It is a real durability threshold - the
	// number of independent failures the write can survive is N-W.
	W int
}

// DefaultQuorum derives a majority write quorum from the replication factor.
func DefaultQuorum(rf int) QuorumConfig {
	return QuorumConfig{
		N: rf,
		W: (rf / 2) + 1,
	}
}

// backgroundOpTimeout bounds work that continues after a request has been
// answered - read repair and post-commit chunk cleanup.
//
// This work deliberately does not use the request's context. If it did, it
// would be cancelled the moment the response is written or the client hangs
// up, which would mean a read repair almost never completes and a delete
// almost never reclaims its chunks. It still needs *some* deadline, or a
// wedged backend would leak a goroutine per request.
const backgroundOpTimeout = 30 * time.Second

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
		if quorum.N < 1 {
			return nil, fmt.Errorf("invalid replication: N(%d) must be at least 1", quorum.N)
		}
		if quorum.W < 1 || quorum.W > quorum.N {
			return nil, fmt.Errorf("invalid write quorum: W(%d) must be between 1 and N(%d)", quorum.W, quorum.N)
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
	fmt.Printf("  - Replication: N=%d, W=%d\n", quorum.N, quorum.W)
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

// onlineBackends resolves node names to the backends that are currently up.
// Names that are unknown or offline are skipped rather than substituted: the
// chunk still belongs on them, and the scrubber will place it there once they
// come back.
func (s *Lilio) onlineBackends(names []string) []StorageBackend {
	var selected []StorageBackend
	for _, name := range names {
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

func (s *Lilio) PutObject(ctx context.Context, bucket, key string, reader io.Reader, size int64, contentType string) (*metadata.ObjectMetadata, error) {
	if !s.Metadata.BucketExists(bucket) {
		return nil, fmt.Errorf("bucket does not exist: %s", bucket)
	}

	startTime := time.Now()

	// Every PutObject mints a fresh object ID, so an overwrite writes an
	// entirely new set of chunk IDs and the previous object's chunks become
	// unreferenced the moment the new metadata commits. Capture them now so
	// they can be reclaimed once that commit has succeeded.
	var superseded []metadata.ChunkInfo
	if prev, err := s.Metadata.GetObjectMetadata(bucket, key); err == nil {
		superseded = prev.Chunks
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

		// Where this chunk belongs, from the ring. This is recorded as-is, even
		// if some of these nodes turn out to be down: it is the expectation the
		// scrubber will later hold the cluster to.
		intendedNodes := s.HashRing.GetNodes(chunkId, s.ReplicationFactor)
		if len(intendedNodes) == 0 {
			return nil, fmt.Errorf("no backends configured for chunk %d", chunkIndex)
		}

		// Of those, the ones we can actually write to right now.
		targetNodes := s.onlineBackends(intendedNodes)
		if len(targetNodes) == 0 {
			return nil, fmt.Errorf("no healthy backends available for chunk %d", chunkIndex)
		}

		// Store chunk on multiple nodes (parallel)
		var successfulNodes []string
		var wg sync.WaitGroup
		var mu sync.Mutex

		for _, node := range targetNodes {
			wg.Add(1)
			go func(b StorageBackend) {
				defer wg.Done()
				if err := b.StoreChunk(ctx, chunkId, chunkData); err == nil {
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
		sort.Strings(intendedNodes)
		chunkInfo := metadata.ChunkInfo{
			ChunkID:      chunkId,
			ChunkIndex:   chunkIndex,
			Size:         int64(originalChunkSize),
			Checksum:     chunkChecksum,
			StorageNodes: intendedNodes,
		}
		chunkInfos = append(chunkInfos, chunkInfo)
		if len(successfulNodes) < len(intendedNodes) {
			fmt.Printf("  ⚠ Chunk %d: %d bytes → stored on %v (under-replicated; belongs on %v)\n",
				chunkIndex, originalChunkSize, successfulNodes, intendedNodes)
		} else {
			fmt.Printf("  ✓ Chunk %d: %d bytes → stored on %v\n", chunkIndex, originalChunkSize, successfulNodes)
		}

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

	// Past the commit point. Reclaiming the replaced chunks only now means a
	// crash at this instant leaks storage, which a later sweep can recover;
	// reclaiming them before the commit would have left the still-current
	// metadata pointing at chunks that no longer exist, which nothing can.
	if len(superseded) > 0 {
		s.deleteChunks(superseded)
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

func (s *Lilio) GetObjectOld(ctx context.Context, bucket, key string) ([]byte, error) {

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

			data, err := backend.RetrieveChunk(ctx, chunkInfo.ChunkID)
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

func (s *Lilio) GetObject(ctx context.Context, bucket, key string, writer io.Writer) error {
	startTime := time.Now()
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
		chunkData, err := s.retrieveChunk(ctx, chunkInfo)
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
	s.Metrics.RecordGetObject(bucket, meta.Size, time.Since(startTime))
	fmt.Printf("  ✓ Object streamed successfully!\n")
	return nil
}

// retrieveChunk returns a chunk's bytes from the first replica that can supply
// a copy matching the checksum recorded in the metadata.
//
// It reads replicas one at a time rather than fanning out to all of them. The
// previous version queried every replica on every read and then required R of
// them to answer, which cost N times the bandwidth of the data being read and
// still refused to serve a verified copy when fewer than R nodes were up. Since
// the checksum is what establishes correctness, one matching copy is proof
// enough, and the rest of the reads are waste.
//
// Replicas are tried in the order given by orderReplicas, so a node believed to
// be up and a backend marked as preferred are consulted first.
func (s *Lilio) retrieveChunk(ctx context.Context, chunkInfo metadata.ChunkInfo) ([]byte, error) {
	candidates := s.orderReplicas(chunkInfo.StorageNodes)

	var corrupt []string
	var unreachable []string
	attempted := 0

	for _, name := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		backend, err := s.Registry.Get(name)
		if err != nil {
			unreachable = append(unreachable, name)
			continue
		}
		attempted++

		data, err := backend.RetrieveChunk(ctx, chunkInfo.ChunkID)
		if err != nil {
			unreachable = append(unreachable, name)
			continue
		}

		if CalculateChecksum(data) != chunkInfo.Checksum {
			corrupt = append(corrupt, name)
			continue
		}

		s.Metrics.RecordChunkRetrieved(name, int64(len(data)))
		s.Metrics.RecordQuorumRead(true, attempted, 1)

		// Any replica that answered with the wrong bytes on the way here is
		// worth fixing now that a good copy is in hand. Replicas that simply
		// did not answer are left to the scrubber, which can tell a node that
		// is missing a chunk from one that is merely unreachable right now.
		if len(corrupt) > 0 {
			go s.readRepair(chunkInfo.ChunkID, data, corrupt)
		}

		return data, nil
	}

	s.Metrics.RecordQuorumRead(false, attempted, 0)
	return nil, fmt.Errorf(
		"chunk %s: no replica could supply data matching its checksum (unreachable: %v, corrupt: %v)",
		chunkInfo.ChunkID, unreachable, corrupt)
}

// orderReplicas sorts a chunk's replica set into the order they should be
// tried: nodes currently believed to be up first, then by backend priority, so
// a fast local disk is preferred over a slow remote one.
func (s *Lilio) orderReplicas(names []string) []string {
	type candidate struct {
		name     string
		online   bool
		priority int
	}

	candidates := make([]candidate, 0, len(names))
	for _, name := range names {
		c := candidate{name: name, priority: int(^uint(0) >> 1)} // unknown backends sort last
		if backend, err := s.Registry.Get(name); err == nil {
			info := backend.Info()
			c.online = info.Status == StatusOnline
			c.priority = info.Priority
		}
		candidates = append(candidates, c)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].online != candidates[j].online {
			return candidates[i].online
		}
		return candidates[i].priority < candidates[j].priority
	})

	ordered := make([]string, 0, len(candidates))
	for _, c := range candidates {
		ordered = append(ordered, c.name)
	}
	return ordered
}

func (s *Lilio) readRepair(chunkId string, data []byte, staleNodes []string) {
	ctx, cancel := context.WithTimeout(context.Background(), backgroundOpTimeout)
	defer cancel()

	for _, nodeName := range staleNodes {
		backend, err := s.Registry.Get(nodeName)
		if err != nil {
			continue
		}

		if err := backend.StoreChunk(ctx, chunkId, data); err == nil {
			fmt.Printf("    🔧 Read repair: fixed %s on %s\n", chunkId, nodeName)
			s.Metrics.RecordReadRepair(nodeName)
		}
	}
}

func (s *Lilio) HeadObject(bucket, key string) (*metadata.ObjectMetadata, error) {
	return s.Metadata.GetObjectMetadata(bucket, key)
}

// DeleteObject removes an object and reclaims its chunks.
//
// Deleting an object that does not exist is not an error: DELETE has to be
// idempotent so that a client which times out and retries does not get a
// failure on the retry, when the first attempt in fact succeeded.
func (s *Lilio) DeleteObject(ctx context.Context, bucket, key string) error {
	meta, err := s.Metadata.GetObjectMetadata(bucket, key)
	if err != nil {
		if errors.Is(err, metadata.ErrObjectNotFound) {
			return nil
		}
		return err
	}

	// The metadata write is the commit point, so it goes first. A crash between
	// the two steps then leaves chunks that nothing references - recoverable by
	// a sweep. The reverse order would leave metadata referencing chunks that
	// have already been deleted: the object would still appear in listings and
	// every read of it would fail, permanently. Leaked storage is a bookkeeping
	// problem; a dangling pointer is data loss.
	if err := s.Metadata.DeleteObjectMetadata(bucket, key); err != nil {
		if errors.Is(err, metadata.ErrObjectNotFound) {
			return nil // lost a race with a concurrent delete; still the desired state
		}
		return err
	}

	s.deleteChunks(meta.Chunks)
	s.Metrics.RecordDeleteObject(bucket)

	return nil
}

// deleteChunks removes chunks from every backend recorded as holding them.
//
// It is best-effort by design. It only ever runs after the metadata that
// referenced these chunks has been removed or replaced, so a failure here leaks
// storage rather than losing data. Returning an error would be misleading -
// the caller's operation has already committed and cannot be undone.
func (s *Lilio) deleteChunks(chunks []metadata.ChunkInfo) {
	// Detached for the same reason as readRepair: this runs after the metadata
	// commit, so a client that hangs up mid-request must not leave the chunks
	// it just orphaned lying around.
	ctx, cancel := context.WithTimeout(context.Background(), backgroundOpTimeout)
	defer cancel()

	for _, chunkInfo := range chunks {
		for _, backendName := range chunkInfo.StorageNodes {
			backend, err := s.Registry.Get(backendName)
			if err != nil {
				continue
			}
			if err := backend.DeleteChunk(ctx, chunkInfo.ChunkID); err != nil {
				fmt.Printf("  ⚠ Failed to reclaim chunk %s on %s: %v\n", chunkInfo.ChunkID, backendName, err)
				continue
			}
			s.Metrics.RecordChunkDeleted(backendName)
		}
	}
}

func (s *Lilio) ListObjects(bucket, prefix string) ([]string, error) {
	return s.Metadata.ListObjects(bucket, prefix)
}

// Storage stats
func (s *Lilio) GetStorageStats(ctx context.Context) map[string]map[string]interface{} {
	stats := make(map[string]map[string]interface{})

	for _, backend := range s.Registry.List() {
		info := backend.Info()
		backendStats, _ := backend.Stats(ctx)

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

func (s *Lilio) HealthCheck(ctx context.Context) map[string]error {
	healthStatus := s.Registry.HealthCheck(ctx)

	// Record backend health metrics
	for nodeName, err := range healthStatus {
		healthy := err == nil
		s.Metrics.RecordBackendHealth(nodeName, healthy)
	}

	return healthStatus
}
