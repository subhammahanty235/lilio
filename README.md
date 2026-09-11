# Lilio

A distributed object store written in Go. You run one coordinator and any
number of storage nodes; it splits objects into chunks, replicates each chunk
across nodes, and repairs replicas that go missing.

It is a learning project, built to understand how storage systems handle
failure rather than to compete with anything. The sections below try to be
accurate about what works, what doesn't, and why particular decisions were
made.

## What it does

- Splits objects into fixed-size chunks and spreads them over storage nodes
  using consistent hashing.
- Replicates each chunk to N nodes and commits only when W of them accept it.
- Repairs replicas that are missing or corrupt, via a scrubber you can run on
  demand or on a timer.
- Mixes storage types freely: a local disk, a spare machine, and a Google Drive
  account can all hold replicas of the same chunk.
- Encrypts per bucket with AES-256-GCM, if you ask it to.
- Exposes an HTTP API, a CLI, a small web UI, and Prometheus metrics.

## Architecture

```
                            clients
                     (CLI, HTTP, web UI)
                              │
                              ▼
                ┌─────────────────────────────┐
                │      lilio server           │
                │       (coordinator)         │
                │                             │
                │  chunking   consistent ring │
                │  encryption placement       │
                │  scrubbing  metadata        │
                └──────────────┬──────────────┘
                               │
          ┌────────────────────┼────────────────────┐
          │ HTTP               │ HTTP               │ local
          ▼                    ▼                    ▼
   ┌─────────────┐      ┌─────────────┐      ┌─────────────┐
   │ lilio-chunkd│      │ lilio-chunkd│      │ local disk  │
   │  machine A  │      │  machine B  │      │ same host   │
   └─────────────┘      └─────────────┘      └─────────────┘
                               │
                               ▼
                         ┌──────────┐
                         │  GDrive  │  (also just a backend)
                         └──────────┘
```

The coordinator owns metadata and every placement decision. Storage nodes are
deliberately dumb: they store a chunk, return a chunk, delete a chunk, and know
nothing about objects, buckets, encryption or replication.

Anything that can store and return bytes is a backend, and they are
interchangeable. `lilio-chunkd` is what turns a plain disk on another machine
into one — S3 and Google Drive are already network services, so they need no
daemon of their own.

Why this shape rather than peer-to-peer: [ADR 0001](docs/adr/0001-coordinator-and-chunk-servers.md).

## How a write works

```
PUT /bucket/key
   │
   ├─ read 1 MB from the request body           (streamed, never buffered whole)
   ├─ encrypt the chunk                          (if the bucket is encrypted)
   ├─ checksum it
   ├─ hash the chunk ID onto the ring → 3 nodes
   ├─ send it to all 3 in parallel
   ├─ at least W accepted?  no → fail the write
   └─ repeat for the next chunk
   │
   └─ write the object's metadata                ← the commit point
```

The metadata write is the commit point, and everything is arranged around it:
data is only ever **added** before it and only ever **removed** after it. A
crash before the commit leaks chunks nobody references — recoverable. The
reverse order would leave metadata pointing at chunks that no longer exist,
which nothing can recover.

Metadata records the full replica set the chunk *belongs* on, including any
node that was down and never received it. That is what makes under-replication
visible later: [ADR 0002](docs/adr/0002-metadata-records-intent.md).

## How a read works

```
GET /bucket/key
   │
   ├─ load metadata
   └─ for each chunk, in order:
        ├─ try replicas: reachable nodes first, then by backend priority
        ├─ does this copy's checksum match the metadata?
        │     yes → use it, stop trying
        │     no  → remember the node, try the next
        ├─ decrypt
        └─ stream to the client
```

There is no read quorum. A chunk is immutable — its ID comes from a fresh
object ID on every PUT and is never overwritten — so every copy is either
byte-identical or damaged, and the checksum says which. One matching copy is
proof enough, so an object stays readable as long as one replica of each chunk
survives intact. Reasoning: [ADR 0003](docs/adr/0003-no-read-quorum.md).

## Replication and repair

Replication alone does not keep data safe, because replicas quietly stop being
replicas: a node is down when a write lands, a disk rots, a directory gets
deleted. None of that produces an error anyone sees.

Read repair does not close this. It only reacts to a replica that answers with
the *wrong bytes* — one that is missing, unreachable, or simply never read is
invisible to it. And a chunk nobody reads is never checked at all, which tends
to be exactly the archived data you assumed was fine.

So there is a scrubber. It walks every object's chunks, asks each node that
should hold one whether it does, and copies a verified replica onto the ones
that are missing or wrong.

```
$ lilio scrub

Scanned 1 objects, 1 chunks in 3ms
  healthy:       0
  repaired:      1 (1 replicas restored)
  unrepairable:  0

  ⚠ vault/f.txt chunk 86c3d67c-..._chunk_0
      belongs on [node-1 node-2 node-3], was intact on [node-1 node-3]
      ✓ restored onto [node-2]
```

Two modes:

| mode | what it asks each node | catches | cost |
|---|---|---|---|
| default | does this chunk exist? | missing replicas | one HEAD per replica |
| `--deep` | send it, and does it checksum? | missing **and** corrupted | transfers the whole dataset |

Repair is a plain copy, so it is idempotent and cannot conflict with anything:
there is exactly one correct value for a given chunk ID, and the checksum
identifies it. Nothing is locked, no metadata is rewritten, and a repair that
fails partway leaves you no worse off than before it started.

Run it on demand (`lilio scrub`), from the API (`POST /admin/scrub`), or on a
timer (see `scrub` in the config). The CLI exits non-zero when a chunk has no
intact copy left, so it works as a cron or monitoring check.

**Orphans are reported, never deleted.** A PUT writes chunks before committing
metadata, so a scrub running in that window would see a live upload's chunks as
garbage. Safe reclamation needs chunk ages and a grace period; until then the
scrub lists them and a human decides.

## Consistency

What Lilio guarantees:

- A write is only visible once its metadata is committed, and metadata is
  committed only after W replicas of every chunk accepted it.
- A read returns bytes matching the checksum recorded at write time, or an
  error. It never returns data it cannot verify.
- Deletes are idempotent.
- An object survives the loss of N−W nodes at write time and remains readable
  while one intact replica of each chunk exists.

What it does not guarantee:

- **Concurrent writes to the same key race.** Both write chunks, both write
  metadata, last one wins, and the loser's chunks are orphaned with no error
  reported to either client. Metadata has no compare-and-swap yet.
- **Nothing is atomic across objects.** There are no multi-object transactions.
- **Metadata is a single point of failure.** Chunks are replicated; the map
  describing them is not. Lose it and the chunks are unreadable bytes.

## Storage backends

| type | what it is | status |
|---|---|---|
| `local` | a directory on the coordinator's own disk | working |
| `remote` | a `lilio-chunkd` on another machine | working |
| `gdrive` | a Google Drive account | working |
| `s3` | S3-compatible object storage | **not implemented** |
| `dropbox`, `sftp` | — | not implemented |

Chunk writes are atomic: written to a temp file and renamed, so a crash leaves
either the previous chunk or nothing, never a truncated file that would still
answer "yes" to an existence check. They are not fsynced, because a chunk's
durability comes from the other replicas and the scrubber — see
[ADR 0004](docs/adr/0004-atomic-writes.md) for the measurements behind that.

## Metadata backends

| type | use | notes |
|---|---|---|
| `local` | default, single machine | JSON files, atomic + fsynced writes |
| `etcd` | multiple coordinators, or when metadata loss is unacceptable | strongly consistent, replicated |
| `memory` | tests | ephemeral |

`local` is the default so that `lilio server` works with no dependencies. Use
`etcd` if you care about the metadata single-point-of-failure noted above.

Object keys are hashed to produce metadata filenames rather than escaped,
because a filename cannot represent an arbitrary key — it is length-limited,
often case-insensitive, and cannot contain a separator. Any escaping scheme
therefore maps some pair of distinct keys onto the same file, and two keys
sharing a file means writing one destroys the other.

## Quick start

### One machine

```bash
go build -o lilio ./cmd/lilio
./lilio init          # writes lilio.json with three local backends
./lilio server
```

```bash
./lilio bucket create photos
./lilio put ./photo.jpg photos/holiday.jpg
./lilio get photos/holiday.jpg ./downloaded.jpg
./lilio ls photos
./lilio scrub
```

### Across machines

On each storage machine:

```bash
go build -o lilio-chunkd ./cmd/lilio-chunkd
./lilio-chunkd --name node-1 --data /mnt/disk --port 9000
```

`lilio-chunkd` is unauthenticated. Bind it to a private network.

Then point the coordinator at them:

```json
{
  "lilio": {
    "chunk_size": "1MB",
    "replication_factor": 3,
    "quorum": { "N": 3, "W": 2 },
    "metadata_path": "./lilio_data/metadata",
    "api_port": 8080
  },
  "scrub": { "enabled": true, "interval": "6h" },
  "storages": [
    { "name": "local-1", "type": "local",  "priority": 1,
      "options": { "path": "./lilio_data/storage/local-1" } },
    { "name": "node-1",  "type": "remote", "priority": 2,
      "options": { "url": "http://192.168.1.41:9000", "timeout": "10s" } },
    { "name": "node-2",  "type": "remote", "priority": 2,
      "options": { "url": "http://192.168.1.42:9000", "timeout": "10s" } }
  ]
}
```

Lower `priority` is preferred, so reads come from the local disk before the
remote machines.

## Configuration

| key | meaning |
|---|---|
| `lilio.chunk_size` | chunk size, e.g. `1MB` |
| `lilio.replication_factor` | copies of each chunk (N) |
| `lilio.quorum.N` / `.W` | replicas, and how many must accept a write |
| `lilio.metadata_path` | where local metadata lives |
| `metadata.type` | `local`, `etcd`, or `memory` |
| `metadata.etcd.endpoints` | etcd cluster addresses |
| `metrics.enabled` / `.type` | Prometheus, or off |
| `scrub.enabled` / `.interval` / `.deep` | periodic repair; off unless set |
| `storages[]` | backend list; see the table above |

`quorum.R` is accepted for backwards compatibility and ignored, with a note at
startup.

## API

| method | path | |
|---|---|---|
| `GET` | `/` | list buckets |
| `PUT` | `/{bucket}` | create bucket (`?encryption=aes256&password=…` to encrypt) |
| `DELETE` | `/{bucket}` | delete an empty bucket |
| `GET` | `/{bucket}?prefix=` | list objects |
| `POST` | `/{bucket}/unlock?password=` | unlock an encrypted bucket |
| `PUT` | `/{bucket}/{key}` | upload |
| `GET` | `/{bucket}/{key}` | download |
| `HEAD` | `/{bucket}/{key}` | metadata only |
| `DELETE` | `/{bucket}/{key}` | delete (idempotent) |
| `GET` | `/admin/stats` | per-backend statistics |
| `GET` | `/admin/health` | backend health |
| `POST` | `/admin/scrub` | run a repair pass (`?deep=true`, `?dry_run=true`) |
| `GET` | `/metrics` | Prometheus |

A failed read is reported honestly. If nothing has been sent yet the server
returns a real status code; if the failure happens mid-stream the connection is
broken rather than returning a short body that looks complete.

## Encryption

Per bucket, AES-256-GCM, key derived from a password with PBKDF2 (100k
iterations) and a random per-bucket salt. Each chunk gets a fresh nonce.

Unlock state lives in the coordinator's memory only, so buckets are locked
again after a restart.

The scrubber never needs the key: chunk checksums cover the bytes as stored,
after encryption.

## Behaviour under failure

| what happens | what Lilio does |
|---|---|
| node down when a write lands | write succeeds if W others accept; the shortfall is logged and visible to a scrub |
| node accepts a connection and never answers | request times out per the backend's `timeout`; it does not hang |
| node returns after an outage | its missing chunks are restored by the next scrub |
| replica silently corrupted | a read that touches it repairs it; `scrub --deep` finds the rest |
| all replicas of a chunk lost | the read fails, and the scrub reports the chunk as unrepairable |
| crash between chunk writes and metadata commit | chunks are orphaned; the object never appears |
| crash between metadata commit and chunk cleanup | old chunks are orphaned; the current object is fine |
| crash mid chunk write | previous chunk survives intact; a temp file is left behind |
| crash mid metadata write | previous metadata survives intact |
| two clients write the same key | **both succeed, last one wins, no error** — a known gap |
| coordinator dies | everything stops; it is a single point of failure |

## Testing

```bash
go test ./...
go test -race ./...
```

Beyond unit tests there are integration tests that run a coordinator against
real chunk servers over real sockets, and failure tests that kill nodes, black-
hole connections, corrupt replicas, and kill a process mid-write to check what
survives on disk.

## Known limitations

- No compare-and-swap on metadata, so concurrent writes to one key race.
- `ListObjects` has no pagination; it loads every object in a bucket.
- Node membership comes from the config file. Adding a node means editing it
  and restarting; there is no failure detection or rebalancing.
- The coordinator is a single point of failure, as is the metadata store when
  it is `local`.
- Orphaned chunks are reported but never reclaimed.
- The S3 backend is a stub.
- No authentication anywhere — neither the API nor `lilio-chunkd`.
- Scrubbing is sequential and unthrottled; on a large store it reads as fast as
  the nodes will allow.
- Logging is `fmt.Printf` to stdout, including a line per chunk.

## Design decisions

- [0001 — A node is a chunk server, not a peer](docs/adr/0001-coordinator-and-chunk-servers.md)
- [0002 — Chunk metadata records intent, not outcome](docs/adr/0002-metadata-records-intent.md)
- [0003 — Lilio has no read quorum](docs/adr/0003-no-read-quorum.md)
- [0004 — Atomic file writes, and where durability comes from](docs/adr/0004-atomic-writes.md)

## License

MIT
