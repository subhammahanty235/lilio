# ADR 0004: Atomic file writes, and where durability comes from

**Status:** accepted
**Date:** 2026-09-12

## Context

Both metadata and chunks were written with `os.WriteFile`, which truncates the
destination and then fills it. Anything interrupting that - a crash, a kill, a
full disk - leaves a short file where a complete one used to be.

For metadata this meant an object whose chunks were all intact could become
permanently unreadable, because the JSON describing it no longer parsed.

For chunks it was subtler and arguably worse. A truncated chunk file still
exists, still has a name, and still answers yes to `HasChunk` - which is
precisely how the default scrub mode decides a replica is healthy. A partial
chunk was therefore counted as intact, and the damage would sit undetected
until someone ran a deep scrub.

## Decision

All file writes go through `internal/fsatomic`: write to a temporary file,
then `rename(2)` onto the destination.

Metadata uses `WriteFile`, which additionally fsyncs the file and its directory.
Chunks use `Replace`, which does not.

## Why the split

Rename and fsync provide different guarantees, and are priced very differently.
Measured on an M1 laptop at the default 1 MB chunk size:

| write path                    | per chunk | throughput |
|-------------------------------|-----------|------------|
| `os.WriteFile` (the old way)  | 0.59 ms   | 1782 MB/s  |
| atomic + fsync                | 9.58 ms   | 110 MB/s   |
| atomic, no fsync              | 1.18 ms   | 886 MB/s   |

Rename gives **atomicity**: the destination is never observed torn. That is
what fixes the bug, and it costs about 2x.

fsync gives **durability**: the bytes survive a power cut. That is the whole
remaining 8x.

For chunks, durability is already provided by something else. A chunk lives on
N nodes, and the scrubber restores any copy a node loses to a power cut. Paying
an fsync per chunk buys a guarantee replication already makes.

Metadata has no replicas to be restored from, so it keeps the fsync. If the
metadata store gains replication - etcd, for instance - that reasoning would be
worth revisiting.

## Trade-offs

A chunk write that is interrupted leaves a temporary file behind. `ListChunks`
skips them, so they are not mistaken for chunks and not reported as orphans by
a scrub; nothing currently reclaims them.

Chunk writes are roughly twice as slow as before. In the deployment this
matters for - a chunk server reached over a network - the transfer dominates
that anyway.

## Consequences

Presence now implies completeness for chunks, which is what makes the cheap
scrub mode trustworthy enough to run on a timer.

A note on testing this: a concurrent-reader test cannot detect the problem,
because `LocalBackendPod` holds a mutex across the write and in-process readers
were already serialised against it. The test that does catch it kills a child
process mid-write and inspects what is left on disk; against the old
implementation it finds a 35 MB truncated chunk.
