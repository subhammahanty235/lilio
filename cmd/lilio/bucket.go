package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	case "unlock":
		bucketUnlock()
	case "info":
		bucketInfo()
	default:
		fmt.Printf("Unknown bucket command: %s\n", subcommand)
		os.Exit(1)
	}
}

func bucketCreate() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: lilio bucket create <name> [options]")
		fmt.Println("\nOptions:")
		fmt.Println("  --encryption    Enable AES-256 encryption")
		fmt.Println("  --password      Encryption password (will prompt if not provided)")
		fmt.Println("\nExamples:")
		fmt.Println("  lilio bucket create photos")
		fmt.Println("  lilio bucket create secrets --encryption")
		fmt.Println("  lilio bucket create secrets --encryption --password mypass")
		os.Exit(1)
	}

	name := os.Args[3]

	createCmd := flag.NewFlagSet("bucket create", flag.ExitOnError)
	server := createCmd.String("server", defaultServer, "Server URL")
	encryption := createCmd.Bool("encryption", false, "Enable AES-256 encryption")
	password := createCmd.String("password", "", "Encryption password")
	createCmd.Parse(os.Args[4:])
	createCmd.Parse(os.Args[4:])

	reqURL := fmt.Sprintf("%s/%s", *server, name)

	if *encryption {
		encPassword := *password

		// Prompt for password if not provided
		if encPassword == "" {
			fmt.Print("Enter encryption password: ")
			fmt.Scanln(&encPassword)

			fmt.Print("Confirm password: ")
			var confirm string
			fmt.Scanln(&confirm)

			if encPassword != confirm {
				fmt.Println("Error: passwords don't match")
				os.Exit(1)
			}
		}

		if len(encPassword) < 8 {
			fmt.Println("Error: password must be at least 8 characters")
			os.Exit(1)
		}

		// Add encryption params to URL
		reqURL += "?encryption=aes256&password=" + url.QueryEscape(encPassword)
	}

	// Create bucket
	// url := fmt.Sprintf("%s/%s", *server, name)
	req, err := http.NewRequest("PUT", reqURL, nil)
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

func bucketUnlock() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: lilio bucket unlock <name>")
		fmt.Println("\nUnlocks an encrypted bucket for read/write operations")
		os.Exit(1)
	}

	name := os.Args[3]

	unlockCmd := flag.NewFlagSet("bucket unlock", flag.ExitOnError)
	server := unlockCmd.String("server", defaultServer, "Server URL")
	password := unlockCmd.String("password", "", "Bucket encryption password")
	unlockCmd.Parse(os.Args[4:])

	pwd := *password
	if pwd == "" {
		fmt.Printf("Enter password for bucket '%s': ", name)
		fmt.Scanln(&pwd)
	}

	reqURL := fmt.Sprintf("%s/%s/unlock?password=%s", *server, name, url.QueryEscape(pwd))
	req, err := http.NewRequest("POST", reqURL, nil)
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

	fmt.Printf("✓ Bucket '%s' unlocked 🔓\n", name)
}

func bucketInfo() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: lilio bucket info <name>")
		os.Exit(1)
	}

	name := os.Args[3]

	infoCmd := flag.NewFlagSet("bucket info", flag.ExitOnError)
	server := infoCmd.String("server", defaultServer, "Server URL")
	infoCmd.Parse(os.Args[4:])

	url := fmt.Sprintf("%s/%s/info", *server, name)
	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Error: %s\n", string(body))
		os.Exit(1)
	}

	var info map[string]interface{}
	json.Unmarshal(body, &info)

	fmt.Printf("\nBucket: %s\n", name)
	fmt.Println("─────────────────────────")

	if enc, ok := info["encryption"].(map[string]interface{}); ok {
		if enabled, ok := enc["enabled"].(bool); ok && enabled {
			fmt.Printf("Encryption: 🔐 %s\n", enc["algorithm"])
		} else {
			fmt.Println("Encryption: None")
		}
	}

	if created, ok := info["created_at"].(string); ok {
		fmt.Printf("Created: %s\n", created)
	}
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
