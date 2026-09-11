# ADR 0003: Lilio has no read quorum

**Status:** accepted
**Date:** 2026-09-10
**Supersedes:** the W+R>N model described in earlier versions of the README

## Context

Lilio required R replicas to answer before a read could succeed, with the
W+R>N rule enforced at startup. With N=3 and R=2, losing two nodes made an
object unreadable - including when the surviving node held a copy whose
checksum matched the metadata exactly. Killing two of three chunk servers and
requesting the object returned HTTP 500 on data that was intact and present.

Two further problems surfaced while examining it:

The check counted nodes that *answered*, not nodes that answered *correctly* -
a response with a failing checksum still counted toward R. The agreement it
advertised was never actually verified.

It read every replica on every read. That was invisible when a replica was a
local directory. It is not invisible when a replica is a machine across a
network or a metered cloud bucket: serving 1 MB cost 3 MB of transfer.

## Options considered

**A. Keep W+R>N.** Matches the Dynamo literature and what the README claimed.

**B. Lower R to 1.** Keeps the machinery, removes the availability cost.

**C. Remove read quorum; serve the first replica whose checksum matches.**

## Decision

Option C.

## Why

A read quorum answers one question: given several replicas of a *mutable* value
that disagree, which is current? Lilio never had that question. A chunk ID is
minted from a fresh object ID on every PUT and is never overwritten, so every
copy of a chunk is either byte-identical to the others or damaged - and the
checksum in the metadata already says which. The overlap guarantee was
protecting an invariant that immutability had already established.

Option B reaches the same runtime behaviour but leaves the concept, the config
surface, and the misleading claim in place. The honest change is to remove the
mechanism and say why.

This is also why `ChunkInfo.Version` was removed in the same change. It was
written on every write and read by nothing, because with immutable chunks there
was never a version for anything to decide.

## Trade-offs

Reads no longer visit every replica, so read repair narrows: it now fixes only
the corrupt replicas a read passed over on its way to a good copy. The general
case - replicas that are missing, unreachable, or simply never read - moves to
the scrubber, which checks every replica of every chunk regardless of traffic.
This ordering is deliberate: repair (ADR 0002 and the scrubber) landed before
this change, so nothing was left uncovered.

Lilio can no longer describe itself as offering tunable quorum consistency.
It offers replication with a write threshold and checksum-verified reads, which
is what it was doing anyway.

## Consequences

An object remains readable as long as one replica of each of its chunks
survives with intact bytes. A read fails only when no replica can produce data
matching the recorded checksum.

`quorum.R` is still accepted in config files so existing deployments keep
loading, and is reported as ignored at startup.
