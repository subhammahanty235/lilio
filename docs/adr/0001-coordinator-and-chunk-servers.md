# ADR 0001: A node is a chunk server, not a peer

**Status:** accepted
**Date:** 2026-09-05

## Context

Lilio called itself distributed, but a "node" was a directory. All three
replicas of a chunk were written by one process, to three folders, usually on
one disk. Consistent hashing, quorum writes, read repair and health checks were
all real code operating over storage that could not become unreachable, could
not time out, and could not disagree about cluster membership.

That meant several distributed-systems mechanisms in the codebase were solving
problems the architecture could not have. Deciding what a node actually is had
to come before repair, rebalancing, or anything else.

## Options considered

**A. A node stays a storage backend.** One process, many backends (local disk,
Google Drive, S3). Honest, already most of the way there, and a real niche:
spreading data across independent providers. But no arrangement of local
directories produces a partition, a timeout, or a node that is slow rather than
dead.

**B. A coordinator plus chunk servers.** The Lilio server keeps metadata, the
ring, and all placement decisions; `lilio-chunkd` daemons hold chunks on their
own disks and answer over HTTP. The GFS/HDFS shape.

**C. Peers.** Every node identical, any node can serve any request, membership
agreed between them by gossip. The Dynamo/Cassandra shape.

**D. C, with each peer owning multiple backends.**

## Decision

Option B.

## Why

Two properties of the existing code made B cheap. Lilio already separated
metadata from data, which is the decision that lets a data tier spread across
machines without the metadata tier caring. And `StorageBackend` was already a
remoting seam: eight methods, five call sites for chunk I/O, one construction
point. A backend that speaks HTTP satisfies the same interface, so the core did
not change at all.

B also avoids the hard part of C. There is exactly one writer of metadata -
the coordinator - so no two participants can disagree about where a chunk
belongs. C spends most of its effort on reaching that agreement without a
leader, and would be solving it a second time given etcd is already a
dependency.

B does not replace A. A remote chunk server and a Google Drive backend sit side
by side in the same registry, because they are the same interface.

## Trade-offs

The coordinator is a single point of failure. This is documented rather than
solved; etcd leader election would make it highly available later without
touching the data path.

The wire protocol is ours to version, and an unauthenticated chunkd is an open
write endpoint - safe on a private network, dangerous anywhere else.

`StorageBackend` had to gain `context.Context`. A local directory either
completes or fails at once; a remote node can accept a connection and then
never answer, and writes wait for all N replicas, so one silent node would hang
the request and the client behind it indefinitely.

## Consequences

Failure modes that previously could not occur now can, and are tested: a node
down at write time, a node that answers slowly rather than not at all, a node
that returns after an outage holding stale data, and the fact that a crashed
node and an unreachable one are indistinguishable to the coordinator.
