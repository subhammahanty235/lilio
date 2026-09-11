# ADR 0002: Chunk metadata records intent, not outcome

**Status:** accepted
**Date:** 2026-09-09

## Context

`ChunkInfo.StorageNodes` listed the nodes that had acknowledged a write. If a
node was down when a chunk was written, it simply did not appear, and the
object looked complete: two entries in a list, no error, nothing amiss.

The result was that under-replication was undetectable. There was no
expectation anywhere in the system for reality to fall short of, so no process
could be written to notice or correct it. Read repair did not help - it only
reacts to a replica that answers with the wrong bytes, so a replica that is
missing, that errors, or that nobody reads was invisible to it.

## Options considered

**A. Record both intent and observed placement.** The metadata would say where
a chunk belongs and which nodes were last confirmed to hold it.

**B. Record intent only, and discover reality when asked.**

**C. Derive intent from the hash ring instead of storing it.** Placement is
deterministic, so `GetNodes(chunkID, RF)` reproduces it.

## Decision

Option B. `StorageNodes` is now the full replica set chosen at write time,
including nodes that were down and never received the chunk.

## Why

Storing observed placement (A) buys nothing, because it is stale the moment a
disk fails. Anything that needs to know which nodes currently hold a chunk has
to ask the nodes, and once it is asking, the stored copy is redundant.

Deriving intent from the ring (C) is correct only while the ring is unchanged.
Adding or removing a node changes what `GetNodes` returns, so existing chunks
would appear misplaced - conflating "this replica is missing" with "this chunk
would be placed differently today". Storing intent pins placement at write time
and keeps those two questions separate.

## Trade-offs

Reads now attempt nodes that may never have received the chunk. This costs a
failed request in the worst case, and is more than repaid by the fact that a
chunk repaired onto its third node is subsequently found there.

An operator reading raw metadata can no longer tell which replicas exist. The
scrubber reports that instead, from live state.

## Consequences

This is what makes repair possible at all: intent and reality can now disagree,
and a scrubber can close the gap. See ADR 0003.
