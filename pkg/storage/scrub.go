package storage

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/subhammahanty235/lilio/pkg/metadata"
)

/*
Scrubbing (anti-entropy)
========================

Replication only protects data if the replicas stay replicated. They don't, on
their own: a node is down when a write lands, a disk develops bad sectors, an
operator deletes the wrong directory. Every one of those quietly reduces the
number of copies, and none of them produces an error anyone sees.

Read repair does not close this gap. It only reacts to a replica that answers
with the *wrong bytes*; a replica that answers with an error, or a node that is
simply unreachable, is skipped and forgotten. And a chunk nobody reads is never
checked at all - which tends to be exactly the archived data you most expected
to still be there.

A scrubber is the background process that goes looking. It walks the metadata,
asks every node that should hold a chunk whether it does, and puts back whatever
is missing or corrupt. It is the difference between "we wrote three copies once"
and "there are three copies right now".

Chunks are immutable - a chunk ID is minted from a fresh object ID on every PUT
and never overwritten - so there is no question of which replica is newest. The
checksum in the metadata is the authority: a copy that matches is correct, a
copy that does not is wrong, and a node without one needs it. That is also why
the scrubber never needs an encryption key, since the checksum covers the bytes
as stored, after encryption.
*/

// ScrubOptions controls one scrub pass.
type ScrubOptions struct {
	// Deep downloads every replica and verifies its checksum, catching silent
	// corruption. It transfers the entire dataset from every node, so it is
	// much more expensive than the default presence check - which only asks
	// each node whether it holds the chunk, and so catches missing replicas
	// but not rotted ones.
	Deep bool

	// DryRun reports what would be repaired without writing anything.
	DryRun bool
}

// ChunkIssue records one chunk that was not in the state the metadata implies.
type ChunkIssue struct {
	Bucket       string   `json:"bucket"`
	Key          string   `json:"key"`
	ChunkID      string   `json:"chunk_id"`
	ExpectedOn   []string `json:"expected_on"`
	HealthyOn    []string `json:"healthy_on"`
	RepairedOn   []string `json:"repaired_on,omitempty"`
	FailedOn     []string `json:"failed_on,omitempty"`
	Unrepairable bool     `json:"unrepairable"`
}

// ScrubReport summarises a pass.
type ScrubReport struct {
	StartedAt time.Time `json:"started_at"`

	// Duration is nanoseconds over the wire, which is how time.Duration
	// marshals; DurationHuman is the same value for anything that has to
	// display it.
	Duration      time.Duration `json:"duration_ns"`
	DurationHuman string        `json:"duration"`

	Deep               bool                `json:"deep"`
	DryRun             bool                `json:"dry_run"`
	ObjectsScanned     int                 `json:"objects_scanned"`
	ChunksScanned      int                 `json:"chunks_scanned"`
	ChunksHealthy      int                 `json:"chunks_healthy"`
	ChunksRepaired     int                 `json:"chunks_repaired"`
	ChunksUnrepairable int                 `json:"chunks_unrepairable"`
	ReplicasRestored   int                 `json:"replicas_restored"`
	Issues             []ChunkIssue        `json:"issues,omitempty"`
	OrphanChunks       map[string][]string `json:"orphan_chunks,omitempty"`
	Errors             []string            `json:"errors,omitempty"`
}

// Healthy reports whether the pass found nothing wrong.
func (r *ScrubReport) Healthy() bool {
	return len(r.Issues) == 0 && r.ChunksUnrepairable == 0 && len(r.Errors) == 0
}

func (r *ScrubReport) String() string {
	return fmt.Sprintf(
		"scrub: %d objects, %d chunks | healthy=%d repaired=%d unrepairable=%d replicas_restored=%d | took %s",
		r.ObjectsScanned, r.ChunksScanned, r.ChunksHealthy, r.ChunksRepaired,
		r.ChunksUnrepairable, r.ReplicasRestored, r.Duration.Round(time.Millisecond))
}

// Scrub walks every object's chunks and restores replicas that are missing or
// corrupt, returning what it found.
//
// It is safe to run at any time. Repair only ever copies a chunk onto a node
// that should already have it, so a scrub racing with a write or a delete can
// at worst re-place a chunk that is about to become garbage - it can never
// remove data. Orphan detection is likewise report-only; see collectOrphans.
func (s *Lilio) Scrub(ctx context.Context, opts ScrubOptions) (*ScrubReport, error) {
	report := &ScrubReport{
		StartedAt: time.Now(),
		Deep:      opts.Deep,
		DryRun:    opts.DryRun,
	}
	defer func() {
		report.Duration = time.Since(report.StartedAt)
		report.DurationHuman = report.Duration.Round(time.Millisecond).String()
	}()

	buckets, err := s.Metadata.ListBuckets()
	if err != nil {
		return nil, fmt.Errorf("failed to list buckets: %w", err)
	}

	referenced := make(map[string]bool)

	for _, bucket := range buckets {
		keys, err := s.Metadata.ListObjects(bucket, "")
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("list objects in %s: %v", bucket, err))
			continue
		}

		for _, key := range keys {
			if err := ctx.Err(); err != nil {
				return report, err
			}

			meta, err := s.Metadata.GetObjectMetadata(bucket, key)
			if err != nil {
				// Most likely deleted while we were scanning; not an error
				// worth failing the pass over.
				report.Errors = append(report.Errors, fmt.Sprintf("read metadata %s/%s: %v", bucket, key, err))
				continue
			}
			report.ObjectsScanned++

			for _, chunk := range meta.Chunks {
				referenced[chunk.ChunkID] = true
				report.ChunksScanned++

				issue := s.scrubChunk(ctx, bucket, key, chunk, opts)
				if issue == nil {
					report.ChunksHealthy++
					continue
				}

				report.Issues = append(report.Issues, *issue)
				report.ReplicasRestored += len(issue.RepairedOn)
				switch {
				case issue.Unrepairable:
					report.ChunksUnrepairable++
				case len(issue.RepairedOn) > 0:
					report.ChunksRepaired++
				}
			}
		}
	}

	report.OrphanChunks = s.collectOrphans(ctx, referenced, report)
	return report, nil
}

// scrubChunk checks one chunk against every node it belongs on, repairing what
// it can. It returns nil when the chunk is fully replicated and intact.
func (s *Lilio) scrubChunk(ctx context.Context, bucket, key string, chunk metadata.ChunkInfo, opts ScrubOptions) *ChunkIssue {
	var healthy, damaged []string
	var goodData []byte

	for _, name := range chunk.StorageNodes {
		backend, err := s.Registry.Get(name)
		if err != nil {
			// The node is not configured on this coordinator at all. That is a
			// missing replica, but not one we can do anything about.
			damaged = append(damaged, name)
			continue
		}

		if opts.Deep {
			data, err := backend.RetrieveChunk(ctx, chunk.ChunkID)
			if err != nil || CalculateChecksum(data) != chunk.Checksum {
				damaged = append(damaged, name)
				continue
			}
			healthy = append(healthy, name)
			if goodData == nil {
				goodData = data
			}
			continue
		}

		if backend.HasChunk(ctx, chunk.ChunkID) {
			healthy = append(healthy, name)
		} else {
			damaged = append(damaged, name)
		}
	}

	if len(damaged) == 0 {
		return nil
	}

	sort.Strings(healthy)
	sort.Strings(damaged)
	issue := &ChunkIssue{
		Bucket:     bucket,
		Key:        key,
		ChunkID:    chunk.ChunkID,
		ExpectedOn: chunk.StorageNodes,
		HealthyOn:  healthy,
	}

	// No surviving copy: this chunk is lost, and the object with it. Reporting
	// it is the whole value here - silent loss is what a scrubber exists to
	// prevent.
	if len(healthy) == 0 {
		issue.Unrepairable = true
		issue.FailedOn = damaged
		return issue
	}

	if opts.DryRun {
		issue.FailedOn = damaged
		return issue
	}

	// A presence-only pass has not read the data yet; fetch a verified copy
	// before writing it anywhere.
	if goodData == nil {
		goodData = s.fetchVerifiedChunk(ctx, chunk, healthy)
		if goodData == nil {
			issue.Unrepairable = true
			issue.FailedOn = damaged
			return issue
		}
	}

	for _, name := range damaged {
		backend, err := s.Registry.Get(name)
		if err != nil {
			issue.FailedOn = append(issue.FailedOn, name)
			continue
		}
		if err := backend.StoreChunk(ctx, chunk.ChunkID, goodData); err != nil {
			issue.FailedOn = append(issue.FailedOn, name)
			continue
		}
		issue.RepairedOn = append(issue.RepairedOn, name)
		s.Metrics.RecordChunkStored(name, int64(len(goodData)))
	}

	return issue
}

// fetchVerifiedChunk returns a copy of the chunk whose bytes match the
// metadata checksum, or nil if no candidate node can supply one.
func (s *Lilio) fetchVerifiedChunk(ctx context.Context, chunk metadata.ChunkInfo, candidates []string) []byte {
	for _, name := range candidates {
		backend, err := s.Registry.Get(name)
		if err != nil {
			continue
		}
		data, err := backend.RetrieveChunk(ctx, chunk.ChunkID)
		if err != nil {
			continue
		}
		// A presence check said this node had the chunk; that is not the same
		// as the bytes being right, so never copy an unverified replica onto
		// another node.
		if CalculateChecksum(data) == chunk.Checksum {
			return data
		}
	}
	return nil
}

// collectOrphans lists chunks present on nodes that no metadata refers to.
//
// It deliberately does not delete them. A PUT writes its chunks before it
// commits metadata, so a scrub running in that window would see a live upload's
// chunks as unreferenced; deleting them would leave the committed metadata
// pointing at nothing. Safe reclamation needs a chunk's age and a grace period
// longer than the slowest possible write, which the backend interface cannot
// currently supply. Until then this reports, and a human decides.
func (s *Lilio) collectOrphans(ctx context.Context, referenced map[string]bool, report *ScrubReport) map[string][]string {
	orphans := make(map[string][]string)

	for _, backend := range s.Registry.List() {
		name := backend.Info().Name

		stored, err := backend.ListChunks(ctx)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("list chunks on %s: %v", name, err))
			continue
		}

		var found []string
		for _, chunkID := range stored {
			if !referenced[chunkID] {
				found = append(found, chunkID)
			}
		}
		if len(found) > 0 {
			sort.Strings(found)
			orphans[name] = found
		}
	}

	if len(orphans) == 0 {
		return nil
	}
	return orphans
}

// RunPeriodicScrub scrubs on an interval until ctx is cancelled. Run it in its
// own goroutine.
//
// The first pass happens after one full interval rather than at startup, so a
// server that is still registering backends does not immediately report every
// chunk on a not-yet-added node as missing.
func (s *Lilio) RunPeriodicScrub(ctx context.Context, interval time.Duration, opts ScrubOptions) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	fmt.Printf("  - Periodic scrub: every %s (deep=%t)\n", interval, opts.Deep)

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			report, err := s.Scrub(ctx, opts)
			if err != nil {
				fmt.Printf("  ⚠ Scrub failed: %v\n", err)
				continue
			}

			fmt.Printf("  %s\n", report)
			if report.ChunksUnrepairable > 0 {
				fmt.Printf("  ✗ %d chunk(s) have no intact copy left - data is lost\n", report.ChunksUnrepairable)
			}
		}
	}
}
