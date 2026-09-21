# ADR 0005: Concurrent writes to one key conflict rather than racing

**Status:** accepted
**Date:** 2026-09-12

## Context

`SaveObjectMetadata` was an unconditional write. Two clients issuing PUT for
the same key both uploaded chunks, both committed metadata, and the later
commit won. The earlier writer received 201 for an object that no longer
existed by the time it was told so, and the chunks it had written were left
referenced by nothing — silently, with no error to either client and no
process that would ever reclaim them.

Reproduced by issuing two 3 MB PUTs to one key at the same time: both returned
201, and the bucket's nodes held two objects' worth of chunks for one object.

## Options considered

**A. Leave it.** Last-write-wins is what S3 does, and clients writing the same
key simultaneously are arguably getting what they deserve.

**B. Lock the key for the duration of a write.** A writer takes a lock,
uploads, commits, releases.

**C. Compare-and-swap on commit.** Each writer notes the revision it read,
uploads, and commits only if the stored revision is unchanged. The loser
removes the chunks it wrote and is told it lost.

## Decision

Option C. `MetadataStore` gains `CompareAndSaveObjectMetadata`, and
`ObjectMetadata` carries a store-assigned `Revision`.

## Why

Option A is defensible for the *visible* semantics but not for the storage
leak. S3 can offer last-write-wins because its garbage collection is somebody
else's problem; here nothing reclaims the loser's chunks, so a key written
concurrently in a loop grows the cluster without bound.

Option B needs a lock that outlives a request and survives the death of its
holder, which means leases, expiry, and deciding what happens when a lock
expires mid-upload. That is a lot of machinery, and it makes an upload's
duration into a liveness problem for everyone else writing that key.

Option C needs no coordination at all. Writers upload optimistically and the
question is settled at the single point where the object becomes visible. It
costs nothing when there is no contention, which is the normal case.

Implementations differ in where the comparison happens, and this is the clearest
argument for running etcd: etcd compares `ModRevision` inside a transaction,
server-side, which is correct across any number of coordinators. The local
store compares under its own mutex, which is only correct while a single
process owns the directory.

## Trade-offs

A concurrent write now fails with HTTP 409 where it used to return 201. That is
a behaviour change, and it is less permissive than S3. It is also the honest
answer: the write genuinely did not survive.

The loser deletes the chunks it uploaded, so a conflict costs the bandwidth of
an upload that is then thrown away. Detecting the conflict earlier would mean
locking, which is option B.

`SaveObjectMetadata` remains for callers that genuinely want an unconditional
write, which makes it possible to use the wrong one by accident.

## Consequences

Chunks are no longer orphaned by concurrent writes. Verified by racing two
PUTs and comparing chunks on disk against chunks referenced by metadata: 15
and 15.
