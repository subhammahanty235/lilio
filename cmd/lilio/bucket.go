package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
)

func handleBucket() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: lilio bucket <create|delete|list> [name]")
		fmt.Println("\nCommands:")
		fmt.Println("  create <n>  Create a new bucket")
		fmt.Println("  delete <n>  Delete a bucket")
		fmt.Println("  list           List all buckets")
		os.Exit(1)
	}

	subcommand := os.Args[2]

	switch subcommand {
	case "create":
		bucketCreate()
	case "delete", "rm":
		bucketDelete()
	case "list", "ls":
		bucketList()
	default:
		fmt.Printf("Unknown bucket command: %s\n", subcommand)
		os.Exit(1)
	}
}

func bucketCreate() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: lilio bucket create <n>")
		os.Exit(1)
	}

	name := os.Args[3]

	createCmd := flag.NewFlagSet("bucket create", flag.ExitOnError)
	server := createCmd.String("server", defaultServer, "Server URL")
	createCmd.Parse(os.Args[4:])

	// Create bucket
	url := fmt.Sprintf("%s/%s", *server, name)
	req, err := http.NewRequest("PUT", url, nil)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		fmt.Println("\nIs the server running? Start with: lilio server")
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error: %s\n", string(body))
		os.Exit(1)
	}

	fmt.Printf("✓ Created bucket: %s\n", name)
}

func bucketDelete() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: lilio bucket delete <n>")
		os.Exit(1)
	}

	name := os.Args[3]

	deleteCmd := flag.NewFlagSet("bucket delete", flag.ExitOnError)
	server := deleteCmd.String("server", defaultServer, "Server URL")
	deleteCmd.Parse(os.Args[4:])

	// Delete bucket
	url := fmt.Sprintf("%s/%s", *server, name)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error: %s\n", string(body))
		os.Exit(1)
	}

	fmt.Printf("✓ Deleted bucket: %s\n", name)
}

func bucketList() {
	listCmd := flag.NewFlagSet("bucket list", flag.ExitOnError)
	server := listCmd.String("server", defaultServer, "Server URL")
	jsonOutput := listCmd.Bool("json", false, "Output as JSON")
	listCmd.Parse(os.Args[3:])

	// List buckets
	resp, err := http.Get(*server + "/")
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

	if *jsonOutput {
		fmt.Println(string(body))
		return
	}

	// Parse response
	var result struct {
		Buckets []string `json:"buckets"`
	}
	json.Unmarshal(body, &result)

	if len(result.Buckets) == 0 {
		fmt.Println("No buckets")
		fmt.Println("\nCreate one with: lilio bucket create mybucket")
		return
	}

	fmt.Println("Buckets:")
	for _, b := range result.Buckets {
		fmt.Printf("  %s\n", b)
	}
	fmt.Printf("\nTotal: %d buckets\n", len(result.Buckets))
}

func handleHealth() {
	healthCmd := flag.NewFlagSet("health", flag.ExitOnError)
	server := healthCmd.String("server", defaultServer, "Server URL")
	jsonOutput := healthCmd.Bool("json", false, "Output as JSON")
	healthCmd.Parse(os.Args[2:])

	// Get stats
	resp, err := http.Get(*server + "/admin/stats")
	if err != nil {
		fmt.Printf("✗ Cannot connect to server: %v\n", err)
		fmt.Println("\nIs the server running? Start with: lilio server")
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if *jsonOutput {
		fmt.Println(string(body))
		return
	}

	// Parse and display
	var stats map[string]map[string]interface{}
	json.Unmarshal(body, &stats)

	if len(stats) == 0 {
		fmt.Println("No storage backends")
		return
	}

	fmt.Println("╔════════════════════════════════════════════╗")
	fmt.Println("║         Storage Backend Health             ║")
	fmt.Println("╠════════════════════════════════════════════╣")

	allHealthy := true
	totalChunks := int64(0)
	totalBytes := int64(0)

	for name, info := range stats {
		status := "✓ online"
		if s, ok := info["status"].(string); ok && s != "online" {
			status = "✗ " + s
			allHealthy = false
		}

		chunks := int64(0)
		if c, ok := info["chunks_stored"].(float64); ok {
			chunks = int64(c)
		}
		totalChunks += chunks

		bytesStored := int64(0)
		if b, ok := info["bytes_stored"].(float64); ok {
			bytesStored = int64(b)
		}
		totalBytes += bytesStored

		fmt.Printf("║  %-12s %s\n", name, status)
		fmt.Printf("║    Chunks: %-8d Stored: %s\n", chunks, formatBytes(bytesStored))
	}

	fmt.Println("╠════════════════════════════════════════════╣")
	fmt.Printf("║  Total: %d backends, %d chunks, %s\n", len(stats), totalChunks, formatBytes(totalBytes))
	fmt.Println("╚════════════════════════════════════════════╝")

	if allHealthy {
		fmt.Println("\n✓ All backends healthy")
	} else {
		fmt.Println("\n⚠ Some backends unhealthy")
		os.Exit(1)
	}
}

func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
