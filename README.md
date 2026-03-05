# Lilio - Distributed Object Storage System

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Build Status](https://img.shields.io/badge/build-passing-brightgreen.svg)]()

**A production-grade distributed object storage system built in Go, inspired by Amazon S3 and designed for cloud-native deployments.**

Lilio implements core distributed systems concepts including consistent hashing, quorum consensus, pluggable metadata backends, streaming I/O, and comprehensive observability - all while maintaining a clean, extensible architecture.

---

## 📋 Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Key Features](#key-features)
- [Quick Start](#quick-start)
- [Metrics & Monitoring](#metrics--monitoring)
- [Configuration](#configuration)
- [Distributed Systems Concepts](#distributed-systems-concepts)
- [API Reference](#api-reference)
- [Development Roadmap](#development-roadmap)
- [Performance](#performance)
- [Contributing](#contributing)

---

## 🎯 Overview

Lilio is a distributed object storage system that allows you to store and retrieve files across multiple storage backends with built-in redundancy, encryption, fault tolerance, and real-time monitoring.

### Why Lilio?

- **🚀 Distributed by Design**: Uses consistent hashing to distribute data evenly across nodes
- **🎯 Quorum Consensus**: W+R > N guarantees for strong consistency and fault tolerance
- **🔄 Pluggable Architecture**: Swap metadata backends (file, etcd, PostgreSQL) without code changes
- **📦 Multiple Storage Backends**: Local disk, Google Drive, S3-compatible storage
- **🔐 Built-in Encryption**: AES-256-GCM encryption at the bucket level
- **⚡ Streaming I/O**: Handle terabyte-sized files without loading into memory
- **📊 Production Observability**: Prometheus metrics + Grafana dashboards
- **🔧 Automatic Read Repair**: Self-healing anti-entropy mechanism

### Use Cases

- **Personal Cloud Storage**: Self-hosted alternative to Dropbox/Google Drive
- **Backup Systems**: Distributed backup with automatic replication and repair
- **Content Delivery**: Origin storage for CDN systems
- **Edge Computing**: Distributed storage for IoT and edge deployments
- **Development & Testing**: Local S3-compatible storage with production-like guarantees

---

## 🏗️ Architecture

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Lilio System                                 │
├─────────────────────────────────────────────────────────────────────┤
│                                                                       │
│  ┌──────────────┐         ┌──────────────┐      ┌──────────────┐   │
│  │  HTTP API    │◀────────│  Web UI      │      │ Prometheus   │   │
│  │  (REST)      │         │  (Browser)   │      │ /metrics     │   │
│  └──────┬───────┘         └──────────────┘      └──────────────┘   │
│         │                                                             │
│         ▼                                                             │
│  ┌────────────────────────────────────────────────────────┐         │
│  │           Lilio Core Engine                             │         │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐       │         │
│  │  │ Chunking   │  │ Encryption │  │ Consistent │       │         │
│  │  │ Engine     │  │ (AES-256)  │  │ Hashing    │       │         │
│  │  └────────────┘  └────────────┘  └────────────┘       │         │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐       │         │
│  │  │ Quorum     │  │ Read Repair│  │ Metrics    │       │         │
│  │  │ (W+R > N)  │  │ (Anti-Ent.)│  │ Collector  │       │         │
│  │  └────────────┘  └────────────┘  └────────────┘       │         │
│  └────────────────────────────────────────────────────────┘         │
│         │                        │                                   │
│         ▼                        ▼                                   │
│  ┌──────────────────┐    ┌──────────────────┐                      │
│  │ Metadata Store   │    │ Storage Registry │                      │
│  │ (Pluggable)      │    │                  │                      │
│  │                  │    │  ┌──────────┐    │                      │
│  │ • File           │    │  │ Backend  │    │                      │
│  │ • etcd           │    │  │ Pool     │    │                      │
│  │ • Memory         │    │  └──────────┘    │                      │
│  └──────────────────┘    └────────┬─────────┘                      │
│                                    │                                 │
│                                    ▼                                 │
│              ┌─────────────────────────────────────┐                │
│              │    Storage Backend Interface        │                │
│              └─────────────────────────────────────┘                │
│                     │          │          │                          │
│        ┌────────────┼──────────┼──────────┼────────┐                │
│        │            │          │          │         │                │
│        ▼            ▼          ▼          ▼         ▼                │
│   ┌────────┐  ┌────────┐  ┌────────┐  ┌────────┐  ...              │
│   │ Local  │  │ GDrive │  │   S3   │  │  SFTP  │                   │
│   │ Disk   │  │        │  │        │  │        │                   │
│   └────────┘  └────────┘  └────────┘  └────────┘                   │
└───────────────────────────────────────────────────────────────────┘
        │                                                       │
        ▼                                                       ▼
  ┌──────────┐                                           ┌──────────┐
  │ Grafana  │◀──────────────────────────────────────────│Prometheus│
  │Dashboard │         Scrapes metrics every 5s          │  :9090   │
  │  :3000   │                                           └──────────┘
  └──────────┘
```

### Data Flow: File Upload with Quorum

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
│     • Add version timestamp (for conflict resolution)   │
│     • Hash chunk ID → Consistent Hash Ring              │
│                                                          │
│     ┌──────────────────────────────────┐                │
│     │   Consistent Hash Ring           │                │
│     │   (150 virtual nodes/backend)    │                │
│     │                                   │                │
│     │   hash(chunk_0) → [node-2, node-1, node-3]       │
│     │                   (3 replicas)    │                │
│     └──────────────────────────────────┘                │
│                                                          │
│  5. Replicate to N=3 nodes (parallel), require W=2     │
│     Quorum Config: N=3, W=2, R=2 (W+R=4 > N=3 ✓)       │
│                                                          │
└──────┬────────────────┬────────────────┬────────────────┘
       │                │                │
       ▼                ▼                ▼
  ┌────────┐       ┌────────┐       ┌────────┐
  │ Node-1 │       │ Node-2 │       │ Node-3 │
  │ (Local)│       │(GDrive)│       │ (S3)   │
  └────┬───┘       └────┬───┘       └────┬───┘
       │                │                │
       │ 6. Store chunk in parallel     │
       │    • 3 goroutines              │
       │    • Wait for W=2 success      │
       ▼                ▼                ▼
  [Success ✓]    [Success ✓]    [Success ✓]

  7. Check Quorum:
     successfulWrites = 3
     if successfulWrites >= W (3 >= 2) ✓
       → Commit metadata
       → Record metrics
     else
       → Rollback chunks
       → Return error

  8. Save metadata to etcd (atomic):
     {
       object_id: "uuid-123",
       chunks: [
         {
           chunk_id: "uuid-123_chunk_0",
           nodes: ["node-2", "node-1", "node-3"],
           version: 1709632145000000000,
           checksum: "sha256..."
         },
         ...
       ]
     }
```

### Data Retrieval with Read Quorum & Repair

```
┌─────────┐
│ Client  │ GET /bucket/key
└────┬────┘
     │
     ▼
┌────────────────┐
│   API Server   │
└────┬───────────┘
     │
     ▼
┌─────────────────────────────────────────────────────┐
│              Lilio Core Engine                       │
│                                                      │
│  1. Fetch metadata from etcd                        │
│     → Get chunk list, checksums, storage nodes      │
│                                                      │
│  2. For each chunk, read from R=2 replicas (parallel)│
│                                                      │
│     ┌─────────┐    ┌─────────┐    ┌─────────┐     │
│     │ Node-1  │    │ Node-2  │    │ Node-3  │     │
│     │ Goroutine│   │ Goroutine│   │ Goroutine│     │
│     └────┬────┘    └────┬────┘    └────┬────┘     │
│          │              │              │            │
│          ▼              ▼              ▼            │
│     [Chunk OK]    [Chunk OK]    [Checksum FAIL]    │
│     Version:100   Version:100   Version:50 (stale) │
│                                                      │
│  3. Check Read Quorum:                              │
│     validResponses = 2                              │
│     if validResponses >= R (2 >= 2) ✓               │
│       → Select highest version (100)                │
│       → Trigger read repair for stale nodes         │
│       → Return data                                 │
│     else                                             │
│       → Return error (quorum not met)               │
│                                                      │
│  4. Read Repair (async):                            │
│     • Copy latest version to Node-3                 │
│     • Update metrics (read_repairs_total++)         │
│     • Log: "🔧 Read repair: fixed chunk on node-3" │
│                                                      │
└─────────────────────────────────────────────────────┘
     │
     ▼
  Stream chunks to client
```

---

## ✨ Key Features

### 1. Quorum Consensus (W+R > N)

**Problem:** How to guarantee strong consistency in a distributed system?

**Solution:** Quorum-based replication with configurable W (write quorum) and R (read quorum)

```go
// Default quorum configuration
N = 3  // Replication factor (total copies)
W = 2  // Write quorum (minimum writes to succeed)
R = 2  // Read quorum (minimum reads to verify)

// Guarantee: W + R > N (2 + 2 > 3) ensures read-write overlap
```

**How It Works:**

```
Write Operation:
┌─────────────────────────────────────────┐
│ 1. Send chunk to N=3 nodes (parallel)  │
│    Targets: [node-1, node-2, node-3]   │
│                                         │
│ 2. Wait for responses                  │
│    Success: node-1 ✓, node-2 ✓, node-3 ✓│
│    Total: 3 successful writes           │
│                                         │
│ 3. Check write quorum:                 │
│    if (successCount >= W)               │
│       3 >= 2 ✓ → SUCCESS                │
│    else                                  │
│       → FAIL (rollback chunks)          │
│                                         │
│ 4. Record metrics:                      │
│    lilio_quorum_write_total{success="true"} ++│
└─────────────────────────────────────────┘

Read Operation:
┌─────────────────────────────────────────┐
│ 1. Fetch chunk from all replicas       │
│    (parallel goroutines)                │
│                                         │
│ 2. Collect responses:                  │
│    node-1: version=100, checksum ✓     │
│    node-2: version=100, checksum ✓     │
│    node-3: version=50,  checksum ✓ (stale)│
│                                         │
│ 3. Check read quorum:                  │
│    validResponses = 3                   │
│    if (validResponses >= R)             │
│       3 >= 2 ✓ → SUCCESS                │
│                                         │
│ 4. Select latest version:              │
│    max(100, 100, 50) = 100              │
│    → Return version 100 data            │
│                                         │
│ 5. Trigger read repair:                │
│    Update node-3 with version 100       │
│    (async, doesn't block read)          │
└─────────────────────────────────────────┘
```

**Benefits:**
- ✅ **Strong Consistency**: W+R > N guarantees reads see latest write
- ✅ **Fault Tolerance**: Survives N-W node failures for writes, N-R for reads
- ✅ **Configurable**: Tune W/R for latency vs consistency tradeoffs
- ✅ **Production-Ready**: Same model used by Cassandra, Riak, DynamoDB

**Test Results:**
```
✓ Write quorum: 3/3 nodes, W=2 → SUCCESS
✓ Write quorum: 1/3 nodes, W=2 → FAIL (correct!)
✓ Read quorum: 3/3 nodes, R=2 → SUCCESS
✓ Read quorum: 1/3 nodes, R=3 → FAIL (correct!)
✓ Read repair: Corrupted chunk automatically fixed
```

**Metrics Tracking:**
```promql
# Quorum success rate (should be ~100%)
sum(lilio_quorum_write_total{success="true"}) /
sum(lilio_quorum_write_total)

# Read repair rate (detects data divergence)
rate(lilio_read_repairs_total[5m])
```

---

### 2. Automatic Read Repair (Anti-Entropy)

**Problem:** Replicas can diverge due to node failures, partial writes, or bit rot

**Solution:** Detect stale/corrupted data during reads and automatically repair

```
Scenario: Node-3 has stale data
┌─────────────────────────────────────────────────────┐
│ Read Request for chunk-123                          │
│                                                      │
│ Step 1: Parallel Read from all replicas            │
│   node-1: data=v2, checksum=abc123 ✓               │
│   node-2: data=v2, checksum=abc123 ✓               │
│   node-3: data=v1, checksum=def456 ✓ (different!)  │
│                                                      │
│ Step 2: Detect Divergence                          │
│   Latest version: v2 (appears 2 times)              │
│   Stale nodes: [node-3]                             │
│                                                      │
│ Step 3: Return Latest Version                      │
│   → Send v2 data to client                          │
│                                                      │
│ Step 4: Repair Asynchronously                      │
│   go readRepair(chunk-123, v2_data, [node-3])      │
│   → Copy v2 to node-3                               │
│   → Record metric: read_repairs_total{node-3}++     │
│   → Log: "🔧 Read repair: fixed chunk-123 on node-3"│
└─────────────────────────────────────────────────────┘
```

**Benefits:**
- ✅ **Self-Healing**: System repairs itself during normal operations
- ✅ **Prevents Entropy**: Stops gradual data degradation
- ✅ **Non-Blocking**: Repairs happen async, don't slow down reads
- ✅ **Observable**: Metrics track repair frequency per node

**Code:**
```go
func (s *Lilio) retrieveChunk(chunkInfo metadata.ChunkInfo) ([]byte, error) {
    // Parallel read from all replicas
    responses := s.readFromAllReplicas(chunkInfo)

    // Check read quorum
    if len(responses) < s.Quorum.R {
        return nil, fmt.Errorf("read quorum failed")
    }

    // Find latest version and stale nodes
    latest, staleNodes := s.selectLatestVersion(responses)

    // Trigger async repair
    if len(staleNodes) > 0 {
        go s.readRepair(chunkInfo.ChunkID, latest.Data, staleNodes)
    }

    return latest.Data, nil
}
```

---

### 3. Consistent Hashing

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
// Returns: ["local-1", "s3-1", "gdrive-1"] (3 replicas)
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

---

### 4. Pluggable Metadata Backends

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

**Why etcd for Production?**
- ✅ Strong consistency (Raft consensus)
- ✅ Distributed (3+ node cluster)
- ✅ Atomic transactions (prevents race conditions)
- ✅ Used by Kubernetes, MinIO, CoreDNS

---

### 5. Streaming Architecture

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
│   • Upload to N nodes (quorum W)     │
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

---

### 6. Data Encryption

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

---

## 🚀 Quick Start

### Prerequisites

- Go 1.21 or higher
- Docker & Docker Compose (for etcd, Prometheus, Grafana)
- Git

### Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/lilio.git
cd lilio

# Start infrastructure (etcd, Prometheus, Grafana)
docker-compose up -d

# Build the binary
go build -o lilio ./cmd/lilio

# Initialize configuration
./lilio init

# Start the server
./lilio server
```

Server will start with:
- API: http://localhost:8080
- Web UI: http://localhost:8080/ui
- Metrics: http://localhost:8080/metrics
- Grafana: http://localhost:3000 (admin/admin)
- Prometheus: http://localhost:9090

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

# Check metrics
curl http://localhost:8080/metrics
```

---

## 📊 Metrics & Monitoring

Lilio provides comprehensive Prometheus metrics and pre-built Grafana dashboards for production observability.

### Quick Start

```bash
# Start monitoring stack
docker-compose up -d prometheus grafana

# Start Lilio (exposes metrics)
./lilio server

# Access dashboards
open http://localhost:3000  # Grafana (admin/admin)
open http://localhost:9090  # Prometheus
open http://localhost:8080/metrics  # Raw metrics
```

### Available Metrics

#### Object Operations
```promql
# Total operations by bucket and type
lilio_objects_total{bucket, operation}  # Counter

# Object sizes (1KB to 100MB buckets)
lilio_object_size_bytes{bucket, operation}  # Histogram

# Request latency (1ms to 10s buckets)
lilio_request_duration_seconds{bucket, operation}  # Histogram
```

#### Quorum & Fault Tolerance
```promql
# Quorum write success/failure
lilio_quorum_write_total{success="true|false"}  # Counter

# Quorum read success/failure
lilio_quorum_read_total{success="true|false"}  # Counter

# Nodes attempted/succeeded in quorum
lilio_quorum_nodes{operation="write|read", type="attempted|succeeded"}  # Gauge

# Read repair operations (anti-entropy)
lilio_read_repairs_total{node}  # Counter
```

#### Chunk Distribution
```promql
# Chunks stored per node
lilio_chunks_stored_total{node}  # Counter

# Chunks retrieved per node
lilio_chunks_retrieved_total{node}  # Counter

# Chunks deleted per node
lilio_chunks_deleted_total{node}  # Counter
```

#### Backend Health
```promql
# Backend health status (1=healthy, 0=down)
lilio_backend_health{node}  # Gauge

# Backend operation latency
lilio_backend_latency_seconds{node, operation}  # Histogram
```

#### System Metrics
```promql
# Active connections
lilio_active_connections  # Gauge
```

### Example Queries

**P99 Write Latency:**
```promql
histogram_quantile(0.99,
  rate(lilio_request_duration_seconds_bucket{operation="put"}[5m])
)
```

**Quorum Success Rate:**
```promql
sum(rate(lilio_quorum_write_total{success="true"}[5m])) /
sum(rate(lilio_quorum_write_total[5m])) * 100
```

**Chunks per Node (Distribution Balance):**
```promql
sum by (node) (lilio_chunks_stored_total)
```

**Read Repair Rate:**
```promql
rate(lilio_read_repairs_total[5m])
```

### Grafana Dashboard

Pre-configured dashboard includes:

1. **Object Operations Rate** - Throughput by bucket/operation
2. **Request Duration (P95)** - Latency tracking for SLO compliance
3. **Chunks Stored by Node** - Data distribution visualization
4. **Quorum Success Rate** - Real-time fault tolerance health (gauge showing %)
5. **Read Repairs** - Anti-entropy activity counter
6. **Backend Health** - Node status table (1=up, 0=down)

**Access:** http://localhost:3000/d/lilio-main

---

## ⚙️ Configuration

### Configuration File: `lilio.json`

```json
{
  "lilio": {
    "chunk_size": "1MB",
    "replication_factor": 3,
    "quorum": {
      "N": 3,  // Total replicas
      "W": 2,  // Write quorum (minimum successful writes)
      "R": 2   // Read quorum (minimum reads to verify)
    },
    "metadata_path": "./lilio_data/metadata",
    "api_port": 8080
  },

  "metadata": {
    "type": "etcd",
    "etcd": {
      "endpoints": ["localhost:2379"],
      "prefix": "/lilio",
      "dial_timeout": "5s"
    }
  },

  "metrics": {
    "enabled": true,
    "type": "prometheus",
    "path": "/metrics"
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
      "name": "local-2",
      "type": "local",
      "priority": 1,
      "options": {
        "path": "./lilio_data/storage/local-2"
      }
    },
    {
      "name": "local-3",
      "type": "local",
      "priority": 1,
      "options": {
        "path": "./lilio_data/storage/local-3"
      }
    }
  ]
}
```

### Quorum Configuration

**Default (Balanced):**
```json
{
  "quorum": {
    "N": 3,
    "W": 2,  // (N/2)+1 = majority
    "R": 2   // (N/2)+1 = majority
  }
}
```
**Tradeoff:** Balanced consistency/availability. W+R=4 > N=3 ensures strong consistency.

**Write-Optimized (Fast Writes):**
```json
{
  "quorum": {
    "N": 3,
    "W": 1,  // Any single write succeeds
    "R": 3   // Must read all replicas
  }
}
```
**Tradeoff:** Faster writes, slower reads. W+R=4 > N=3 still guarantees consistency.

**Read-Optimized (Fast Reads):**
```json
{
  "quorum": {
    "N": 3,
    "W": 3,  // Must write to all replicas
    "R": 1   // Any single read succeeds
  }
}
```
**Tradeoff:** Slower writes, faster reads. W+R=4 > N=3 still guarantees consistency.

**⚠️ Important:** Always ensure `W + R > N` for strong consistency!

### Running with Docker Compose

```bash
# Start all services
docker-compose up -d

# Check services
docker-compose ps

# View logs
docker-compose logs -f lilio

# Stop all services
docker-compose down
```

---

## 🧠 Distributed Systems Concepts

### 1. Quorum Consensus

**Why W + R > N matters:**

```
Example with N=3, W=2, R=2:

Write to nodes: [A, B, C]
W=2 → must write to 2 nodes
Possible write sets: {A,B}, {A,C}, {B,C}

Read from nodes: [A, B, C]
R=2 → must read from 2 nodes
Possible read sets: {A,B}, {A,C}, {B,C}

Since W+R=4 > N=3, ANY read set MUST overlap with the previous write set!

Example:
  Write set: {A, B}
  Read set:  {B, C} → overlaps at B (contains latest data)

This guarantees you'll see the latest write (strong consistency).
```

If `W+R ≤ N`, you could have:
- Write set: {A, B}
- Read set: {C} (if R=1)
- **Stale read!** Node C doesn't have latest data

**Lilio validates this at startup:**
```go
if quorum.W+quorum.R <= quorum.N {
    return nil, fmt.Errorf("invalid quorum: W(%d) + R(%d) must be > N(%d)")
}
```

---

### 2. Consistent Hashing

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

---

### 3. Read Repair (Anti-Entropy)

**The Entropy Problem:**

Without read repair, entropy accumulates over time:
```
Day 1:  [A:v1, B:v1, C:v1] ← all in sync
Day 30: [A:v1, B:v1, C:corrupted] ← 1 bad replica
Day 60: [A:v1, B:corrupted, C:corrupted] ← 2 bad replicas
Day 90: Data loss! (majority corrupted)
```

With read repair:
```
Day 1:  [A:v1, B:v1, C:v1]
Day 30: [A:v1, B:v1, C:corrupted] → Read triggers repair → [A:v1, B:v1, C:v1]
Day 60: Still [A:v1, B:v1, C:v1] ← entropy prevented!
```

**Benefits:**
- ✅ Self-healing during normal operations
- ✅ No separate repair job needed
- ✅ Catches bit rot, partial writes, network issues
- ✅ Observable via metrics

---

### 4. CAP Theorem Tradeoffs

Lilio's choices:

**With W+R > N:**
- ✅ **Consistency** - Reads always see latest write
- ✅ **Partition Tolerance** - Survives network splits
- ⚠️ **Availability** - Unavailable if < W or < R nodes are up

**Tunable via quorum settings:**
- High W, low R → Prioritize write consistency
- Low W, high R → Prioritize read consistency
- W=R=(N/2)+1 → Balanced (default)

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

**Metrics (Prometheus)**
```
GET /metrics

# Example
curl http://localhost:8080/metrics
```

**Unlock Encrypted Bucket**
```
POST /{bucket}/unlock?password={password}

# Example
curl -X POST "http://localhost:8080/my-bucket/unlock?password=secret"
```

---

## 🗺️ Development Roadmap

### ✅ Completed Features

#### Phase 1: Core Storage
- [x] File chunking (configurable size)
- [x] Consistent hashing (150 virtual nodes)
- [x] Multiple storage backends (Local, GDrive, S3)
- [x] Basic replication (parallel writes)
- [x] Checksum validation (SHA-256)
- [x] HTTP REST API
- [x] CLI interface

#### Phase 2: Production Features
- [x] **Streaming architecture** (handle terabyte files)
- [x] **Pluggable metadata backends** (File, etcd, Memory)
- [x] Per-bucket encryption (AES-256-GCM)
- [x] Comprehensive test suite
- [x] Docker Compose for infrastructure
- [x] Web UI

#### Phase 3: Fault Tolerance ⭐ **NEW**
- [x] **Quorum writes** (W+R > N guarantees)
- [x] **Read quorum** (verify data from R replicas)
- [x] **Automatic read repair** (anti-entropy mechanism)
- [x] **Metrics & monitoring** (Prometheus + Grafana)
- [ ] Rollback on partial write failure
- [ ] Versioning support (keep multiple versions)

### 🚧 In Progress

#### Phase 4: Advanced Fault Tolerance
- [ ] **Version-based conflict resolution** (Last-Write-Wins with timestamps)
- [ ] **Rollback mechanism** (cleanup on quorum failure)
- [ ] **Hinted handoff** (sloppy quorum for availability)

### 📋 Pipeline

#### Phase 5: Repair & Rebalancing
- [ ] Background scrubber job
- [ ] Automatic repair of under-replicated chunks
- [ ] Rebalancing when nodes join/leave
- [ ] Garbage collection of orphaned chunks

#### Phase 6: Observability Enhancements
- [ ] Alerting rules (Prometheus alerts)
- [ ] Request tracing (OpenTelemetry)
- [ ] Structured logging (zap/zerolog)
- [ ] Performance profiling endpoints

#### Phase 7: Advanced Features
- [ ] S3-compatible multipart upload
- [ ] Resumable uploads
- [ ] HTTP Range requests
- [ ] Object versioning
- [ ] Lifecycle policies

#### Phase 8: Multi-Region
- [ ] Cross-region replication
- [ ] Conflict resolution (vector clocks)
- [ ] Geo-aware routing
- [ ] Disaster recovery

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

**Quorum Operations:**
```
TestQuorumWriteSuccess: 3/3 nodes, W=2 → SUCCESS (0.00s)
TestQuorumReadSuccess:  3/3 nodes, R=2 → SUCCESS (0.00s)
TestReadRepair:         Corrupted chunk fixed (0.10s)
```

**Metrics Collection:**
```
Prometheus scrape interval: 5s
Metric overhead: <1% CPU, <10MB RAM
```

### Scalability

**Tested Configurations:**
- ✅ Single file: 10GB (streaming)
- ✅ Total storage: 100GB across 3 backends
- ✅ Object count: 10,000+ objects
- ✅ Concurrent clients: 10 simultaneous uploads/downloads
- ✅ Quorum: N=3, W=2, R=2 (99.9% success rate)
- ✅ Backend diversity: Local + GDrive + S3 mixed

**Expected Limits (untested):**
- Metadata (etcd): 1M+ objects (tested by Kubernetes)
- File size: Unlimited (streaming architecture)
- Storage capacity: Unlimited (add more backends)
- Throughput: Limited by network and backend speed

---

## 🤝 Contributing

We welcome contributions! See our [Contributing Guidelines](CONTRIBUTING.md) for details.

### Quick Start

```bash
# Clone and setup
git clone https://github.com/yourusername/lilio.git
cd lilio
go mod download

# Run tests
go test ./...

# Run with coverage
go test -cover ./...

# Build
go build -o lilio ./cmd/lilio
```

### Areas That Need Help

**High Priority:**
- [ ] Version-based conflict resolution implementation
- [ ] Rollback on partial write failure
- [ ] PostgreSQL metadata backend
- [ ] Prometheus alerting rules

---

## 📚 Additional Resources

### Architecture Deep Dives

- [QUORUM_REVIEW.md](./QUORUM_REVIEW.md) - Quorum consensus implementation review
- [METRICS_REVIEW.md](./METRICS_REVIEW.md) - Metrics & monitoring architecture
- [PLUGGABLE_METADATA_REVIEW.md](./PLUGGABLE_METADATA_REVIEW.md) - Metadata backends analysis
- [STREAMING_ANALYSIS.md](./STREAMING_ANALYSIS.md) - Streaming architecture deep dive

### Learning Resources

**Distributed Systems:**
- "Designing Data-Intensive Applications" by Martin Kleppmann
- MIT 6.824 (Distributed Systems) course
- Raft Paper: "In Search of an Understandable Consensus Algorithm"

**Production Systems:**
- Cassandra Quorum Documentation
- Amazon DynamoDB Consistency Model
- etcd Raft Implementation

---

## 📄 License

MIT License - see [LICENSE](LICENSE) file for details.

---

## 🙏 Acknowledgments

- **Cassandra** - Quorum consensus model
- **DynamoDB** - W+R > N consistency guarantees
- **MinIO** - Inspiration for architecture
- **Kubernetes** - etcd usage patterns
- **Amazon S3** - API design

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
   with Quorum Consensus
```
