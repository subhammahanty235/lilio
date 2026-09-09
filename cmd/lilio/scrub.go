package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
)

// scrubReport mirrors the JSON returned by POST /admin/scrub.
type scrubReport struct {
	Duration           string              `json:"duration"`
	Deep               bool                `json:"deep"`
	DryRun             bool                `json:"dry_run"`
	ObjectsScanned     int                 `json:"objects_scanned"`
	ChunksScanned      int                 `json:"chunks_scanned"`
	ChunksHealthy      int                 `json:"chunks_healthy"`
	ChunksRepaired     int                 `json:"chunks_repaired"`
	ChunksUnrepairable int                 `json:"chunks_unrepairable"`
	ReplicasRestored   int                 `json:"replicas_restored"`
	OrphanChunks       map[string][]string `json:"orphan_chunks"`
	Issues             []struct {
		Bucket       string   `json:"bucket"`
		Key          string   `json:"key"`
		ChunkID      string   `json:"chunk_id"`
		ExpectedOn   []string `json:"expected_on"`
		HealthyOn    []string `json:"healthy_on"`
		RepairedOn   []string `json:"repaired_on"`
		FailedOn     []string `json:"failed_on"`
		Unrepairable bool     `json:"unrepairable"`
	} `json:"issues"`
	Errors []string `json:"errors"`
}

func handleScrub() {
	scrubCmd := flag.NewFlagSet("scrub", flag.ExitOnError)
	server := scrubCmd.String("server", defaultServer, "Server URL")
	deep := scrubCmd.Bool("deep", false, "Download every replica and verify its checksum (slow, catches corruption)")
	dryRun := scrubCmd.Bool("dry-run", false, "Report what would be repaired without writing anything")
	jsonOut := scrubCmd.Bool("json", false, "Print the raw report")
	scrubCmd.Parse(os.Args[2:])

	url := fmt.Sprintf("%s/admin/scrub?deep=%t&dry_run=%t", *server, *deep, *dryRun)

	if *deep {
		fmt.Println("Running a deep scrub - every replica is downloaded and verified. This may take a while.")
	} else {
		fmt.Println("Scrubbing (presence check). Use --deep to also detect corrupted replicas.")
	}

	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		fmt.Println("\nIs the server running? Start with: lilio server")
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Error: %s\n", string(body))
		os.Exit(1)
	}
	if *jsonOut {
		fmt.Println(string(body))
		return
	}

	var report scrubReport
	if err := json.Unmarshal(body, &report); err != nil {
		fmt.Printf("Could not parse the report: %v\n", err)
		os.Exit(1)
	}
	printScrubReport(&report)

	// Non-zero exit when data is actually lost, so this is usable from a cron
	// job or a monitoring check.
	if report.ChunksUnrepairable > 0 {
		os.Exit(2)
	}
}

func printScrubReport(r *scrubReport) {
	fmt.Printf("\nScanned %d objects, %d chunks in %s\n", r.ObjectsScanned, r.ChunksScanned, r.Duration)
	fmt.Printf("  healthy:       %d\n", r.ChunksHealthy)
	fmt.Printf("  repaired:      %d (%d replicas restored)\n", r.ChunksRepaired, r.ReplicasRestored)
	fmt.Printf("  unrepairable:  %d\n", r.ChunksUnrepairable)

	if r.DryRun {
		fmt.Println("\n  (dry run - nothing was written)")
	}

	for _, issue := range r.Issues {
		if issue.Unrepairable {
			fmt.Printf("\n  ✗ DATA LOST %s/%s chunk %s\n", issue.Bucket, issue.Key, issue.ChunkID)
			fmt.Printf("      belongs on %v, no intact copy on any of them\n", issue.ExpectedOn)
			continue
		}
		fmt.Printf("\n  ⚠ %s/%s chunk %s\n", issue.Bucket, issue.Key, issue.ChunkID)
		fmt.Printf("      belongs on %v, was intact on %v\n", issue.ExpectedOn, issue.HealthyOn)
		if len(issue.RepairedOn) > 0 {
			fmt.Printf("      ✓ restored onto %v\n", issue.RepairedOn)
		}
		if len(issue.FailedOn) > 0 {
			fmt.Printf("      ✗ could not restore onto %v\n", issue.FailedOn)
		}
	}

	if len(r.OrphanChunks) > 0 {
		total := 0
		for _, ids := range r.OrphanChunks {
			total += len(ids)
		}
		fmt.Printf("\n  %d chunk(s) on disk that no object refers to:\n", total)
		for node, ids := range r.OrphanChunks {
			fmt.Printf("      %s: %d\n", node, len(ids))
		}
		fmt.Println("      These are not deleted automatically. A chunk from an upload that is")
		fmt.Println("      still in flight looks exactly the same, and deleting it would destroy")
		fmt.Println("      the object being written.")
	}

	for _, e := range r.Errors {
		fmt.Printf("\n  error: %s\n", e)
	}

	if r.ChunksUnrepairable == 0 && len(r.Issues) == 0 && len(r.Errors) == 0 {
		fmt.Println("\n✓ Everything is where it should be.")
	}
	fmt.Println()
}
