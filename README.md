# Lilio - Distributed Object Storage System

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Build Status](https://img.shields.io/badge/build-passing-brightgreen.svg)]()

**A production-grade distributed object storage system built in Go, inspired by Amazon S3 and designed for cloud-native deployments.**

Lilio implements core distributed systems concepts including consistent hashing, pluggable metadata backends, data replication, and streaming I/O - all while maintaining a clean, extensible architecture.

---

## 📋 Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Key Features](#key-features)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Distributed Systems Concepts](#distributed-systems-concepts)
- [API Reference](#api-reference)
- [Development Roadmap](#development-roadmap)
- [Contributing](#contributing)
- [Performance](#performance)

---

## 🎯 Overview

Lilio is a distributed object storage system that allows you to store and retrieve files across multiple storage backends with built-in redundancy, encryption, and scalability.

### Why Lilio?

- **🚀 Distributed by Design**: Uses consistent hashing to distribute data evenly across nodes
- **🔄 Pluggable Architecture**: Swap metadata backends (file, etcd, PostgreSQL) without code changes
- **📦 Multiple Storage Backends**: Local disk, Google Drive, S3-compatible storage
- **🔐 Built-in Encryption**: AES-256-GCM encryption at the bucket level
- **⚡ Streaming I/O**: Handle terabyte-sized files without loading into memory
- **🎛️ Production-Ready**: Proper error handling, health checks, and observability hooks

### Use Cases

- **Personal Cloud Storage**: Self-hosted alternative to Dropbox/Google Drive
- **Backup Systems**: Distributed backup with automatic replication
- **Content Delivery**: Origin storage for CDN systems
- **Edge Computing**: Distributed storage for IoT and edge deployments
- **Development & Testing**: Local S3-compatible storage for development

---

## 🏗️ Architecture

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Lilio System                             │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────┐         ┌──────────────┐                     │
│  │  HTTP API    │◀────────│  Web UI      │                     │
│  │  (REST)      │         │  (Browser)   │                     │
│  └──────┬───────┘         └──────────────┘                     │
│         │                                                        │
│         ▼                                                        │
│  ┌─────────────────────────────────────────────────────┐       │
│  │           Lilio Core Engine                          │       │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐   │       │
│  │  │ Chunking   │  │ Encryption │  │ Consistent │   │       │
│  │  │ Engine     │  │ (AES-256)  │  │ Hashing    │   │       │
│  │  └────────────┘  └────────────┘  └────────────┘   │       │
│  └─────────────────────────────────────────────────────┘       │
│         │                        │                              │
│         ▼                        ▼                              │
│  ┌──────────────────┐    ┌──────────────────┐                 │
│  │ Metadata Store   │    │ Storage Registry │                 │
│  │ (Pluggable)      │    │                  │                 │
│  │                  │    │  ┌──────────┐    │                 │
│  │ • File           │    │  │ Backend  │    │                 │
│  │ • etcd           │    │  │ Pool     │    │                 │
│  │ • PostgreSQL     │    │  └──────────┘    │                 │
│  └──────────────────┘    └────────┬─────────┘                 │
│                                    │                            │
│                                    ▼                            │
│              ┌─────────────────────────────────────┐           │
│              │    Storage Backend Interface        │           │
│              └─────────────────────────────────────┘           │
│                     │          │          │                     │
│        ┌────────────┼──────────┼──────────┼────────┐          │
│        │            │          │          │         │          │
│        ▼            ▼          ▼          ▼         ▼          │
│   ┌────────┐  ┌────────┐  ┌────────┐  ┌────────┐  ...        │
│   │ Local  │  │ GDrive │  │   S3   │  │  SFTP  │             │
│   │ Disk   │  │        │  │        │  │        │             │
│   └────────┘  └────────┘  └────────┘  └────────┘             │
└──────────────────────────────────────────────────────────────┘
```

### Data Flow: File Upload

```
┌─────────┐
│ Client  │
└────┬────┘
     │
     │ 1. HTTP PUT /bucket/key (file: 10MB)
     ▼
┌────────────────┐
│   API Server   │
└────┬───────────┘
     │
     │ 2. Stream to Lilio.PutObject()
     ▼
┌─────────────────────────────────────────────────────────┐
│                  Lilio Core Engine                       │
│                                                          │
│  3. ChunkReader (1MB chunks)                            │
│     ┌────┬────┬────┬────┬────┬────┬────┬────┬────┬────┐│
│     │ C0 │ C1 │ C2 │ C3 │ C4 │ C5 │ C6 │ C7 │ C8 │ C9 ││
│     └────┴────┴────┴────┴────┴────┴────┴────┴────┴────┘│
│                                                          │
│  4. For each chunk:                                     │
│     • Encrypt (if bucket encrypted)                     │
│     • Calculate checksum (SHA-256)                      │
│     • Hash chunk ID → Consistent Hash Ring              │
│                                                          │
│     ┌──────────────────────────────┐                    │
│     │   Consistent Hash Ring       │                    │
│     │   (150 virtual nodes/backend)│                    │
│     │                               │                    │
│     │   hash(chunk_0) → node-2     │                    │
│     │   hash(chunk_1) → node-1     │                    │
│     │   hash(chunk_2) → node-3     │                    │
│     └──────────────────────────────┘                    │
│                                                          │
│  5. Replicate to N nodes (parallel)                     │
│     Replication Factor = 2                              │
│                                                          │
└──────┬────────────────┬────────────────┬────────────────┘
       │                │                │
       ▼                ▼                ▼
  ┌────────┐       ┌────────┐       ┌────────┐
  │ Node-1 │       │ Node-2 │       │ Node-3 │
  │ (Local)│       │(GDrive)│       │ (S3)   │
  └────┬───┘       └────┬───┘       └────┬───┘
       │                │                │
       │ 6. Store chunk on disk/cloud   │
       ▼                ▼                ▼
  [C1, C4, C7]    [C0, C3, C6]    [C2, C5, C8]

  7. Save metadata to etcd/file/postgres
     {
       object_id: "uuid-123",
       chunks: [
         {chunk_id: "uuid-123_chunk_0", nodes: ["node-2", "node-1"]},
         {chunk_id: "uuid-123_chunk_1", nodes: ["node-1", "node-3"]},
         ...
       ]
     }
```

### Consistent Hashing in Action

```
                    Hash Ring (0 to 2^32)

        0 ┌─────────────────────────────────┐ 2^32
          │                                 │
          │  Virtual Nodes (150 per backend)│
          │                                 │
    Node-3│         ●  ●●   ●   ●●          │Node-1
     VN450│        ●  ●  ● ●   ●  ●●        │VN150
          │       ●  ●    ●   ●     ●       │
          │      ●  ●                ●      │
          │     ●                      ●    │
          │    ●    Node-1 (150 VN)     ●  │
          │   ●                           ●●│
          │  ●                             ●│
          │ ●                               │
    Node-2│●                                │Node-3
     VN300│●●  ●  ●   ●●    ●●   ●   ●  ●  │VN450
          │  ●   ●  ●    ●●    ●   ●   ●   │
          │   ●   ●   ●      ●   ●   ●     │
          │         Node-2 (150 VN)        │
          │                                 │
          └─────────────────────────────────┘

Hash Function: SHA-256(chunk_id) → position on ring
Lookup: Binary search for first VN ≥ position
Result: Maps to physical node

Example:
  chunk_0 → hash = 12345678 → Node-2 (primary)
                             → Node-3 (replica)

Benefits:
  ✓ Even distribution (each node gets ~1/N of data)
  ✓ Minimal redistribution when nodes join/leave (~1/N keys move)
  ✓ No central coordinator needed
  ✓ Deterministic (same chunk always goes to same nodes)
```

---

## ✨ Key Features

### 1. Consistent Hashing

**Problem:** How to distribute chunks evenly across storage nodes?

**Solution:** Consistent hashing with virtual nodes

```go
// Hash ring with 150 virtual nodes per backend
hashRing := hashing.NewHashRing(150)
hashRing.AddNode("local-1")
hashRing.AddNode("gdrive-1")
hashRing.AddNode("s3-1")

// Distribute chunk
nodes := hashRing.GetNodes(chunkID, replicationFactor)
// Returns: ["local-1", "s3-1"] (2 replicas)
```

**Benefits:**
- ✅ Even distribution (proven: 22-28% per node with 4 nodes)
- ✅ Minimal redistribution (~18% keys move when adding 5th node)
- ✅ No hotspots or load imbalance
- ✅ Works with heterogeneous backends (different sizes/speeds)

**Test Results:**
```
Distribution with 4 nodes, 10,000 keys:
  node-1: 2502 keys (25.0%)  ✓
  node-2: 2800 keys (28.0%)  ✓
  node-3: 2286 keys (22.9%)  ✓
  node-4: 2412 keys (24.1%)  ✓

Adding 5th node:
  Keys redistributed: 183/1000 (18.3%)  ✓ Optimal ~20%
```

### 2. Pluggable Metadata Backends

**Problem:** Single point of failure with file-based metadata

**Solution:** Interface-based abstraction with multiple implementations

```
┌──────────────────────────────────────────────────────┐
│          MetadataStore Interface                     │
├──────────────────────────────────────────────────────┤
│  • CreateBucket(name string) error                   │
│  • SaveObjectMetadata(meta) error                    │
│  • GetObjectMetadata(bucket, key) (*Meta, error)     │
│  • ListObjects(bucket, prefix) ([]string, error)     │
│  • Health() error                                     │
└────────┬─────────────────┬─────────────────┬─────────┘
         │                 │                 │
    ┌────▼────┐      ┌─────▼──────┐    ┌────▼────┐
    │  File   │      │    etcd    │    │ Memory  │
    │         │      │            │    │         │
    │  Dev    │      │ Production │    │ Testing │
    └─────────┘      └────────────┘    └─────────┘
```

**Implementations:**

| Backend | Use Case | Distributed | Consistency |
|---------|----------|-------------|-------------|
| **File** | Development, single-node | ❌ No | Strong (single node) |
| **etcd** | Production, multi-node | ✅ Yes | Strong (Raft consensus) |
| **Memory** | Testing, CI/CD | ❌ No | N/A (ephemeral) |

**Configuration:**
```json
// Local development
{
  "metadata": {
    "type": "local",
    "local": {"path": "./lilio_data/metadata"}
  }
}

// Production with etcd
{
  "metadata": {
    "type": "etcd",
    "etcd": {
      "endpoints": ["etcd-1:2379", "etcd-2:2379", "etcd-3:2379"],
      "prefix": "/lilio"
    }
  }
}
```

**Why etcd for Production?**
- ✅ Strong consistency (Raft consensus)
- ✅ Distributed (3+ node cluster)
- ✅ Watch API (real-time updates)
- ✅ Transactions (atomic operations)
- ✅ Used by Kubernetes, MinIO, CoreDNS

### 3. Streaming Architecture

**Problem:** Large files (>1GB) cause out-of-memory errors

**Solution:** Chunk-by-chunk streaming with constant memory usage

```
Traditional Approach (BAD):
┌──────────────────────────────────────┐
│ Load entire 10GB file into memory   │ 💥 OOM!
│      ↓                               │
│ Encrypt entire file                  │ 💥 20GB RAM
│      ↓                               │
│ Chunk into pieces                    │
│      ↓                               │
│ Upload chunks                        │
└──────────────────────────────────────┘
Peak Memory: 3-4× file size

Lilio Streaming Approach (GOOD):
┌──────────────────────────────────────┐
│ FOR EACH 1MB chunk:                  │ ✅ 1MB RAM
│   • Read chunk from stream           │
│   • Encrypt chunk                    │
│   • Upload chunk                     │
│   • Free memory (GC)                 │
│ REPEAT                               │
└──────────────────────────────────────┘
Peak Memory: 1× chunk size (~1MB)
```

**Performance:**
```
File Size    Old Memory    New Memory    Improvement
---------    ----------    ----------    -----------
10 MB        ~30 MB        ~1 MB         30x
100 MB       ~300 MB       ~1 MB         300x
1 GB         OOM!          ~1 MB         ∞
10 GB        Crash         ~1 MB         ∞

Benchmark Results:
  Throughput: 13.48 GB/s
  Allocations: 6 per 1MB file
  Memory/op: 1.3 MB
```

### 4. Data Encryption

**Algorithm:** AES-256-GCM (Authenticated Encryption)

```
Encryption Flow:
┌─────────────────────────────────────────────────────────┐
│                                                          │
│  1. User creates bucket with password                   │
│     lilio bucket create photos --encrypt --password=*** │
│                                                          │
│  2. Derive key from password                            │
│     salt ← random(16 bytes)                             │
│     key  ← PBKDF2(password, salt, 100k iterations)      │
│                                                          │
│  3. Store encryption metadata                           │
│     {                                                    │
│       "enabled": true,                                   │
│       "algorithm": "aes256-gcm",                         │
│       "salt": base64(salt),                             │
│       "key_hash": sha256(password)  // for verification │
│     }                                                    │
│                                                          │
│  4. Encrypt each chunk                                  │
│     nonce ← random(12 bytes)  // unique per chunk      │
│     ciphertext ← AES-GCM-Encrypt(key, nonce, chunk)     │
│     output ← nonce || ciphertext || tag                 │
│                                                          │
│  5. Decrypt on retrieval                                │
│     nonce ← ciphertext[:12]                             │
│     data  ← AES-GCM-Decrypt(key, nonce, ciphertext[12:])│
│     ✓ Tag verification prevents tampering               │
│                                                          │
└─────────────────────────────────────────────────────────┘
```

**Security Features:**
- ✅ Per-bucket encryption (granular control)
- ✅ Password-based key derivation (PBKDF2, 100k iterations)
- ✅ Authenticated encryption (GCM mode prevents tampering)
- ✅ Unique nonce per chunk (prevents pattern analysis)
- ✅ Random salt per bucket (prevents rainbow tables)

### 5. Data Replication

**Configuration:**
```json
{
  "lilio": {
    "replication_factor": 2  // Store each chunk on 2 nodes
  }
}
```

**Replication Strategy:**
```
Chunk Distribution (replication_factor = 2):

Chunk-0:
  hash(chunk-0) → position 1234567
  Ring lookup  → [node-2, node-3]
  Store on both nodes in parallel

Chunk-1:
  hash(chunk-1) → position 9876543
  Ring lookup  → [node-1, node-2]
  Store on both nodes in parallel

Retrieval with Failover:
  1. Try primary node (node-2)
  2. Verify checksum
  3. If fail → Try replica (node-3)
  4. Return first valid chunk
```

**Fault Tolerance:**
```
Scenario: Node-2 fails

Before:
  Chunk-0: [node-2 ✓, node-3 ✓]  → 2 copies
  Chunk-1: [node-1 ✓, node-2 ✓]  → 2 copies

After Node-2 Failure:
  Chunk-0: [node-2 ✗, node-3 ✓]  → 1 copy (retrievable)
  Chunk-1: [node-1 ✓, node-2 ✗]  → 1 copy (retrievable)

✓ Data still accessible (failover to healthy replica)
⚠ Reduced redundancy (needs repair job to re-replicate)
```

**Current Limitations:**
- ⚠️ No quorum writes (succeeds with 1/N successful writes)
- ⚠️ No automatic repair (under-replicated chunks not detected)
- ⚠️ No rebalancing (new nodes don't get existing data)

**Roadmap:** See [Planned: Repair & Rebalancing](#6-repair-and-rebalancing-pipeline)

---

## 🚀 Quick Start

### Prerequisites

- Go 1.21 or higher
- Docker & Docker Compose (optional, for etcd)
- Git

### Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/lilio.git
cd lilio

# Build the binary
go build -o lilio ./cmd/lilio

# Initialize configuration
./lilio init

# Start the server
./lilio server
```

### Basic Usage

```bash
# Create a bucket
./lilio bucket create my-bucket

# Upload a file
./lilio put /path/to/local/file.txt my-bucket/file.txt

# Download a file
./lilio get my-bucket/file.txt /path/to/output.txt

# List objects in bucket
./lilio ls my-bucket

# Delete object
./lilio rm my-bucket/file.txt

# Check backend health
./lilio health
```

### Using HTTP API

```bash
# Start server
./lilio server --port 8080

# Create bucket
curl -X PUT http://localhost:8080/my-bucket

# Upload file
curl -X PUT http://localhost:8080/my-bucket/photo.jpg \
  --data-binary @photo.jpg

# Download file
curl http://localhost:8080/my-bucket/photo.jpg -o photo.jpg

# List objects
curl http://localhost:8080/my-bucket

# Get stats
curl http://localhost:8080/admin/stats
```

### Web UI

Open browser to `http://localhost:8080/ui`

---

## ⚙️ Configuration

### Configuration File: `lilio.json`

```json
{
  "lilio": {
    "chunk_size": "1MB",           // Size of each chunk
    "replication_factor": 2,        // Number of replicas per chunk
    "metadata_path": "./lilio_data/metadata",
    "api_port": 8080
  },

  "metadata": {
    "type": "etcd",                 // "local", "etcd", "memory"
    "etcd": {
      "endpoints": ["localhost:2379", "localhost:22379", "localhost:32379"],
      "prefix": "/lilio",
      "dial_timeout": "5s"
    }
  },

  "storages": [
    {
      "name": "local-1",
      "type": "local",
      "priority": 1,
      "options": {
        "path": "./lilio_data/storage/local-1"
      }
    },
    {
      "name": "gdrive-1",
      "type": "gdrive",
      "priority": 10,
      "options": {
        "credentials": "./gdrive-credentials.json"
      }
    },
    {
      "name": "s3-1",
      "type": "s3",
      "priority": 5,
      "options": {
        "endpoint": "s3.amazonaws.com",
        "bucket": "my-lilio-bucket",
        "region": "us-east-1"
      }
    }
  ]
}
```

### Storage Backend Options

#### Local Filesystem
```json
{
  "name": "local-1",
  "type": "local",
  "priority": 1,
  "options": {
    "path": "./storage/local-1"
  }
}
```

#### Google Drive
```json
{
  "name": "gdrive-1",
  "type": "gdrive",
  "priority": 10,
  "options": {
    "credentials": "./gdrive-credentials.json",
    "folder_id": "optional-folder-id"
  }
}
```

#### Amazon S3
```json
{
  "name": "s3-1",
  "type": "s3",
  "priority": 5,
  "options": {
    "endpoint": "s3.amazonaws.com",
    "bucket": "my-bucket",
    "region": "us-east-1",
    "access_key": "your-access-key",
    "secret_key": "your-secret-key"
  }
}
```

### Metadata Backend Options

#### File-based (Development)
```json
{
  "metadata": {
    "type": "local",
    "local": {
      "path": "./lilio_data/metadata"
    }
  }
}
```

#### etcd (Production)
```json
{
  "metadata": {
    "type": "etcd",
    "etcd": {
      "endpoints": ["etcd-1:2379", "etcd-2:2379", "etcd-3:2379"],
      "prefix": "/lilio",
      "username": "optional-user",
      "password": "optional-pass"
    }
  }
}
```

### Running with etcd

```bash
# Start 3-node etcd cluster
docker-compose up -d

# Verify etcd is running
docker-compose ps

# Configure Lilio to use etcd (edit lilio.json)
# Then start Lilio
./lilio server
```

---

## 🧠 Distributed Systems Concepts

### 1. Consistent Hashing

**Why we use it:**
Traditional hashing (`node = hash(key) % N`) causes massive data redistribution when nodes are added/removed.

```
Traditional Hashing:
  3 nodes → Add 4th node → 75% of keys move!  ❌

Consistent Hashing:
  3 nodes → Add 4th node → ~18-20% of keys move!  ✅
```

**Implementation:**
- SHA-256 hash function
- 150 virtual nodes per physical node (reduces variance)
- Binary search for O(log n) lookup
- Deterministic placement (same key → same nodes)

**Code:**
```go
// pkg/hashing/consistent.go
type HashRing struct {
    ring            []uint32              // Sorted positions
    positionToNode  map[uint32]string     // Position → node name
    nodeToPositions map[string][]uint32   // Node → virtual positions
    virtualNodes    int                    // 150 per node
}

func (hr *HashRing) GetNodes(key string, count int) []string {
    position := hash(key)
    // Binary search for first position >= our position
    idx := sort.Search(len(hr.ring), func(i int) bool {
        return hr.ring[i] >= position
    })
    // Walk clockwise, collecting unique nodes
    // ...
}
```

### 2. Data Replication

**CAP Theorem Tradeoffs:**

Lilio chooses **AP** (Availability + Partition Tolerance) over **C** (Consistency):
- ✅ System stays available during network partitions
- ✅ Writes succeed even if some nodes are down
- ⚠️ Eventual consistency (replicas may temporarily diverge)

**Replication Flow:**
```go
// Parallel replication
var wg sync.WaitGroup
var mu sync.Mutex
var successfulNodes []string

for _, node := range targetNodes {
    wg.Add(1)
    go func(backend StorageBackend) {
        defer wg.Done()
        if err := backend.StoreChunk(chunkID, data); err == nil {
            mu.Lock()
            successfulNodes = append(successfulNodes, backend.Info().Name)
            mu.Unlock()
        }
    }(node)
}

wg.Wait()

// ⚠️ Current: Succeeds with ANY successful write
if len(successfulNodes) == 0 {
    return fmt.Errorf("failed to store chunk")
}
```

**Planned: Quorum Writes**
```go
// Future: Require W = ⌈N/2⌉ successful writes
requiredWrites := (replicationFactor / 2) + 1
if len(successfulNodes) < requiredWrites {
    return fmt.Errorf("quorum not achieved")
}
```

### 3. Metadata Consensus (etcd)

**Why etcd:**
- Uses **Raft consensus algorithm** for strong consistency
- Guarantees linearizable reads/writes
- Handles leader election automatically
- Survives minority failures (2/3 nodes down = still works)

**Raft in Action:**
```
3-Node etcd Cluster:

Normal Operation:
  Client → Leader → Replicate to followers → Commit

  ┌────────┐     ┌────────┐     ┌────────┐
  │ etcd-1 │────▶│ etcd-2 │────▶│ etcd-3 │
  │ Leader │◀────│Follower│◀────│Follower│
  └────────┘     └────────┘     └────────┘
       │              │              │
       └──────────────┴──────────────┘
              Majority ACK

Leader Failure:
  etcd-2 times out → Election → etcd-2 becomes leader

  ┌────────┐     ┌────────┐     ┌────────┐
  │ etcd-1 │     │ etcd-2 │────▶│ etcd-3 │
  │  DOWN  │     │ Leader │◀────│Follower│
  └────────┘     └────────┘     └────────┘
                      │              │
                      └──────────────┘
                      Still works! (2/3 alive)
```

**Lilio's etcd Usage:**
```go
// Atomic bucket creation (prevents race conditions)
txn := client.Txn(ctx)
txn = txn.If(clientv3.Compare(clientv3.Version(key), "=", 0))
txn = txn.Then(clientv3.OpPut(key, value))

resp, err := txn.Commit()
if !resp.Succeeded {
    return errors.New("bucket already exists")
}
```

### 4. Failure Handling

**Node Failure Detection:**
```go
func (s *Lilio) HealthCheck() map[string]error {
    unhealthy := make(map[string]error)
    for name, backend := range s.Registry.List() {
        if err := backend.Health(); err != nil {
            unhealthy[name] = err
            // Mark as offline
            backend.SetStatus(StatusOffline)
        }
    }
    return unhealthy
}
```

**Retrieval with Failover:**
```go
func (s *Lilio) retrieveChunk(chunkInfo metadata.ChunkInfo) ([]byte, error) {
    for _, nodeName := range chunkInfo.StorageNodes {
        backend, err := s.Registry.Get(nodeName)
        if err != nil {
            continue  // Try next replica
        }

        data, err := backend.RetrieveChunk(chunkInfo.ChunkID)
        if err != nil {
            continue  // Try next replica
        }

        // Verify integrity
        if CalculateChecksum(data) == chunkInfo.Checksum {
            return data, nil  // Success!
        }
    }
    return nil, errors.New("all replicas failed or corrupted")
}
```

**Checksums for Integrity:**
- Per-chunk checksums (detect corruption)
- Full-file checksum (verify complete retrieval)
- Automatic failover to replica if checksum mismatch

---

## 📡 API Reference

### REST API Endpoints

#### Bucket Operations

**Create Bucket**
```
PUT /{bucket}

# Example
curl -X PUT http://localhost:8080/my-bucket

# With encryption
curl -X PUT "http://localhost:8080/my-bucket?encryption=aes256&password=secret"
```

**List Buckets**
```
GET /

# Example
curl http://localhost:8080/
```

**Delete Bucket**
```
DELETE /{bucket}

# Example
curl -X DELETE http://localhost:8080/my-bucket
```

**List Objects in Bucket**
```
GET /{bucket}?prefix={prefix}

# Example
curl http://localhost:8080/my-bucket?prefix=photos/
```

#### Object Operations

**Upload Object**
```
PUT /{bucket}/{key}

# Example
curl -X PUT http://localhost:8080/my-bucket/photo.jpg \
  -H "Content-Type: image/jpeg" \
  --data-binary @photo.jpg
```

**Download Object**
```
GET /{bucket}/{key}

# Example
curl http://localhost:8080/my-bucket/photo.jpg -o downloaded.jpg
```

**Delete Object**
```
DELETE /{bucket}/{key}

# Example
curl -X DELETE http://localhost:8080/my-bucket/photo.jpg
```

**Get Object Metadata**
```
HEAD /{bucket}/{key}

# Example
curl -I http://localhost:8080/my-bucket/photo.jpg
```

#### Admin Operations

**Storage Statistics**
```
GET /admin/stats

# Example
curl http://localhost:8080/admin/stats
```

**Backend Health**
```
GET /admin/health

# Example
curl http://localhost:8080/admin/health
```

**Unlock Encrypted Bucket**
```
POST /{bucket}/unlock?password={password}

# Example
curl -X POST "http://localhost:8080/my-bucket/unlock?password=secret"
```

### CLI Commands

```bash
# Initialize configuration
lilio init

# Server management
lilio server [--port PORT] [--host HOST]

# Bucket operations
lilio bucket create <name> [--encrypt] [--password PASSWORD]
lilio bucket delete <name>
lilio bucket list
lilio bucket unlock <name> --password PASSWORD

# Object operations
lilio put <local-path> <bucket/key>
lilio get <bucket/key> <local-path>
lilio ls <bucket> [prefix]
lilio rm <bucket/key>

# Storage backend management
lilio storage add <name> --type TYPE [options]
lilio storage remove <name>
lilio storage list

# Health check
lilio health
```

---

## 🗺️ Development Roadmap

### ✅ Completed Features

#### Phase 1: Core Storage (Complete)
- [x] File chunking (configurable size)
- [x] Consistent hashing (150 virtual nodes)
- [x] Multiple storage backends (Local, GDrive, S3)
- [x] Basic replication (parallel writes)
- [x] Checksum validation (SHA-256)
- [x] HTTP REST API
- [x] CLI interface

#### Phase 2: Production Features (Complete)
- [x] **Streaming architecture** (handle terabyte files)
- [x] **Pluggable metadata backends** (File, etcd, Memory)
- [x] Per-bucket encryption (AES-256-GCM)
- [x] Comprehensive test suite
- [x] Docker Compose for etcd
- [x] Web UI

### 🚧 In Progress

#### Phase 3: Fault Tolerance (Week 1-2)
- [ ] **Quorum writes** (require W=⌈N/2⌉ successful writes)
- [ ] **Read quorum** (verify data from R replicas)
- [ ] **Rollback on partial write failure**
- [ ] **Versioning support** (keep multiple versions of objects)

### 📋 Pipeline

#### Phase 4: Repair & Rebalancing (Week 3-4)

**Scrubber Service:**
```go
// Periodically verify and repair under-replicated chunks
type Scrubber struct {
    interval time.Duration  // How often to scan
    batchSize int           // Chunks to process per batch
}

func (s *Scrubber) Run(ctx context.Context) {
    ticker := time.NewTicker(s.interval)
    for {
        select {
        case <-ticker.C:
            s.scanAndRepair()
        case <-ctx.Done():
            return
        }
    }
}

func (s *Scrubber) scanAndRepair() {
    // 1. List all objects from metadata
    // 2. For each chunk, verify:
    //    - All replicas exist
    //    - Checksums match
    //    - Correct replication factor
    // 3. Re-replicate if needed
    // 4. Delete extra replicas (over-replication)
}
```

**Rebalancing:**
```go
// When new node joins, move data to balance load
func (s *Lilio) Rebalance(newNode string) error {
    // 1. Add node to hash ring
    s.HashRing.AddNode(newNode)

    // 2. Identify chunks that should move to new node
    chunksToMove := s.identifyChunksForRebalancing(newNode)

    // 3. Copy chunks to new node
    for _, chunkID := range chunksToMove {
        s.copyChunkToNode(chunkID, newNode)
    }

    // 4. Update metadata
    // 5. Delete old replicas
}
```

**Roadmap Items:**
- [ ] Background scrubber job
- [ ] Automatic repair of under-replicated chunks
- [ ] Rebalancing when nodes join/leave
- [ ] Garbage collection of orphaned chunks
- [ ] Checksum verification of existing data

#### Phase 5: Observability (Week 5-6)

**Metrics (Prometheus):**
```go
// Counter metrics
metrics.ChunkWrites.Inc()
metrics.ChunkReads.Inc()
metrics.FailedWrites.Inc()

// Histogram metrics
metrics.UploadLatency.Observe(duration.Seconds())
metrics.ChunkSize.Observe(float64(len(data)))

// Gauge metrics
metrics.BackendHealth.WithLabelValues(nodeName).Set(1) // 1=healthy, 0=down
metrics.TotalChunks.Set(float64(count))
```

**Structured Logging:**
```go
import "go.uber.org/zap"

logger.Info("chunk uploaded",
    zap.String("chunk_id", chunkID),
    zap.String("backend", backendName),
    zap.Int64("size", size),
    zap.Duration("latency", duration),
)
```

**Roadmap Items:**
- [ ] Prometheus metrics endpoint
- [ ] Structured logging (zap/zerolog)
- [ ] Request tracing (OpenTelemetry)
- [ ] Health check dashboard
- [ ] Alerting rules (under-replication, node failures)
- [ ] Grafana dashboard templates

#### Phase 6: Advanced Features (Month 3)

**Multipart Upload API:**
```bash
# Initiate multipart upload
POST /{bucket}/{key}?uploads
→ {"upload_id": "xyz123"}

# Upload parts
PUT /{bucket}/{key}?upload_id=xyz123&part=1
PUT /{bucket}/{key}?upload_id=xyz123&part=2

# Complete upload
POST /{bucket}/{key}?upload_id=xyz123&complete
Body: {"parts": [{"part": 1, "etag": "..."}, {"part": 2, "etag": "..."}]}
```

**Roadmap Items:**
- [ ] S3-compatible multipart upload
- [ ] Resumable uploads (resume interrupted transfers)
- [ ] HTTP Range requests (partial downloads)
- [ ] Object versioning (keep history)
- [ ] Lifecycle policies (auto-delete old versions)
- [ ] Access control lists (bucket/object permissions)
- [ ] Signed URLs (temporary access tokens)

#### Phase 7: Performance Optimization (Month 4)

**Roadmap Items:**
- [ ] Connection pooling (reuse HTTP connections)
- [ ] Chunk caching (LRU cache for hot chunks)
- [ ] Compression (compress before encryption, optional)
- [ ] Adaptive chunk sizing (larger chunks for sequential access)
- [ ] Parallel uploads/downloads (multiple chunks concurrently)
- [ ] Client-side deduplication

#### Phase 8: Multi-Region (Month 5-6)

**Geo-Replication:**
```
Region: US-East           Region: EU-West
┌──────────────┐         ┌──────────────┐
│ Lilio-US-1   │◀───────▶│ Lilio-EU-1   │
│ Lilio-US-2   │  Sync   │ Lilio-EU-2   │
│ Lilio-US-3   │         │ Lilio-EU-3   │
└──────────────┘         └──────────────┘
       │                        │
       ▼                        ▼
  etcd cluster            etcd cluster
```

**Roadmap Items:**
- [ ] Cross-region replication
- [ ] Conflict resolution (last-write-wins, vector clocks)
- [ ] Geo-aware routing (read from nearest region)
- [ ] Disaster recovery (full region failover)

---

## 🧪 Performance

### Benchmarks

**Consistent Hashing:**
```
BenchmarkHashRingLookup-8        8555361   121.8 ns/op    0 B/op   0 allocs/op
BenchmarkHashRingReplication-8   4640528   254.7 ns/op   48 B/op   1 allocs/op
```

**Streaming I/O:**
```
BenchmarkChunkReaderThroughput-8    1464   777μs/op   13.48 GB/s   11.5MB/op   12 allocs/op
BenchmarkChunkReaderAllocation-8   10000   117μs/op    1.31MB/op    6 allocs/op
```

**Metadata Operations:**
```
BenchmarkLocalStore_SaveObject-8    50000   31245 ns/op   2048 B/op   12 allocs/op
BenchmarkMemoryStore_SaveObject-8  500000    2103 ns/op    512 B/op    8 allocs/op
```

### Scalability

**Tested Configurations:**
- ✅ Single file: 10GB (streaming)
- ✅ Total storage: 100GB across 3 backends
- ✅ Object count: 10,000+ objects
- ✅ Concurrent clients: 10 simultaneous uploads/downloads
- ✅ Backend diversity: Local + GDrive + S3 mixed

**Expected Limits (untested):**
- Metadata (etcd): 1M+ objects (tested by Kubernetes)
- File size: Unlimited (streaming architecture)
- Storage capacity: Unlimited (add more backends)
- Throughput: Limited by network and backend speed

---

## 🤝 Contributing

We welcome contributions! Here's how to get started:

### Development Setup

```bash
# Clone repository
git clone https://github.com/yourusername/lilio.git
cd lilio

# Install dependencies
go mod download

# Run tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run specific test
go test -v ./pkg/hashing -run TestConsistentHashing

# Build
go build -o lilio ./cmd/lilio
```

### Code Organization

```
lilio/
├── cmd/
│   └── lilio/          # CLI entry point
├── pkg/
│   ├── api/            # HTTP API server
│   ├── config/         # Configuration management
│   ├── crypto/         # Encryption (AES-256-GCM)
│   ├── hashing/        # Consistent hashing
│   ├── metadata/       # Metadata backends (file, etcd, memory)
│   ├── storage/        # Core storage engine + backend interface
│   │   └── storage-models/  # Backend implementations (local, gdrive, s3)
│   ├── utils/          # Utilities (ChunkReader, etc.)
│   └── web/            # Web UI assets
└── docs/               # Documentation
```

### Testing Guidelines

**All code must have tests:**
- Unit tests for core logic
- Integration tests for backends
- Benchmark tests for performance-critical paths

**Test conventions:**
```go
// Unit test
func TestChunkData(t *testing.T) { ... }

// Benchmark
func BenchmarkChunkData(b *testing.B) { ... }

// Integration test (may skip in CI)
func TestEtcdIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }
    // ...
}
```

### Pull Request Process

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Write tests for your changes
4. Ensure all tests pass (`go test ./...`)
5. Format code (`go fmt ./...`)
6. Commit with clear message (`git commit -m 'Add amazing feature'`)
7. Push to branch (`git push origin feature/amazing-feature`)
8. Open Pull Request

### Areas That Need Help

**High Priority:**
- [ ] PostgreSQL metadata backend
- [ ] Quorum write implementation
- [ ] Repair/rebalancing service
- [ ] Prometheus metrics
- [ ] Multi-region support

**Medium Priority:**
- [ ] S3-compatible API (full compatibility)
- [ ] Web UI improvements
- [ ] CLI UX improvements
- [ ] Documentation improvements

**Good First Issues:**
- [ ] Add more unit tests
- [ ] Improve error messages
- [ ] Add configuration validation
- [ ] Write tutorials/examples

---

## 📚 Additional Resources

### Architecture Deep Dives

- [CONSISTENT_HASHING_ANALYSIS.md](./CONSISTENT_HASHING_ANALYSIS.md) - Deep dive into hashing implementation
- [STREAMING_ANALYSIS.md](./STREAMING_ANALYSIS.md) - Streaming architecture review
- [PLUGGABLE_METADATA_REVIEW.md](./PLUGGABLE_METADATA_REVIEW.md) - Metadata architecture analysis

### Learning Resources

**Distributed Systems:**
- "Designing Data-Intensive Applications" by Martin Kleppmann
- "Distributed Systems" by Maarten van Steen
- MIT 6.824 (Distributed Systems) course

**Consensus Algorithms:**
- Raft Paper: "In Search of an Understandable Consensus Algorithm"
- etcd Documentation: https://etcd.io/docs/

**Production Systems:**
- MinIO Architecture: https://min.io/
- Kubernetes etcd Usage: https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/
- Amazon S3 API Reference: https://docs.aws.amazon.com/s3/

---

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

---

## 🙏 Acknowledgments

- **MinIO** - Inspiration for pluggable metadata architecture
- **Kubernetes** - etcd usage patterns
- **Amazon S3** - API design
- **Consistent Hashing** - Research papers on distributed hash tables

---

## 📞 Contact & Support

- **Issues:** [GitHub Issues](https://github.com/subhammahanty235/lilio/issues)
- **Email:** subhammahanty235@gmail.com

---

**Built with ❤️ in Go**

```
     _     _ _ _
    | |   (_) (_)
    | |    _| |_  ___
    | |   | | | |/ _ \
    | |___| | | | (_) |
    |_____|_|_|_|\___/

   Distributed Object Storage
```
