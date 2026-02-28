package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var defaultServer = "http://localhost:8080"

func handlePut() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: lilio put <local-file> <bucket/key>")
		fmt.Println("\nExamples:")
		fmt.Println("  lilio put ./photo.jpg photos/vacation/photo.jpg")
		fmt.Println("  lilio put ./document.pdf docs/report.pdf --server http://localhost:9000")
		os.Exit(1)
	}

	localPath := os.Args[2]
	remotePath := os.Args[3]

	putCmd := flag.NewFlagSet("put", flag.ExitOnError)
	server := putCmd.String("server", defaultServer, "Server URL")
	putCmd.Parse(os.Args[4:])

	// Parse bucket/key
	parts := strings.SplitN(remotePath, "/", 2)
	if len(parts) != 2 {
		fmt.Println("Error: remote path must be in format bucket/key")
		fmt.Println("Example: lilio put ./file.txt mybucket/file.txt")
		os.Exit(1)
	}
	bucket, key := parts[0], parts[1]

	// Check local file exists
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		fmt.Printf("Error: file not found: %s\n", localPath)
		os.Exit(1)
	}

	// Read local file
	data, err := os.ReadFile(localPath)
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Uploading %s (%d bytes)...\n", localPath, len(data))

	// Upload
	url := fmt.Sprintf("%s/%s/%s", *server, bucket, key)
	req, err := http.NewRequest("PUT", url, bytes.NewReader(data))
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	req.Header.Set("Content-Type", getContentType(localPath))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("Error uploading: %v\n", err)
		fmt.Println("\nIs the server running? Start with: lilio server")
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error: %s\n", string(body))
		os.Exit(1)
	}

	fmt.Printf("✓ Uploaded to %s/%s\n", bucket, key)
}

func handleGet() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: lilio get <bucket/key> <local-file>")
		fmt.Println("\nExamples:")
		fmt.Println("  lilio get photos/vacation/photo.jpg ./downloaded.jpg")
		fmt.Println("  lilio get docs/report.pdf ./report.pdf --server http://localhost:9000")
		os.Exit(1)
	}

	remotePath := os.Args[2]
	localPath := os.Args[3]

	getCmd := flag.NewFlagSet("get", flag.ExitOnError)
	server := getCmd.String("server", defaultServer, "Server URL")
	getCmd.Parse(os.Args[4:])

	// Parse bucket/key
	parts := strings.SplitN(remotePath, "/", 2)
	if len(parts) != 2 {
		fmt.Println("Error: remote path must be in format bucket/key")
		os.Exit(1)
	}
	bucket, key := parts[0], parts[1]

	fmt.Printf("Downloading %s/%s...\n", bucket, key)

	// Download
	url := fmt.Sprintf("%s/%s/%s", *server, bucket, key)
	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		fmt.Println("\nIs the server running? Start with: lilio server")
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error: %s\n", string(body))
		os.Exit(1)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		os.Exit(1)
	}

	// Create directory if needed
	dir := filepath.Dir(localPath)
	if dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Printf("Error creating directory: %v\n", err)
			os.Exit(1)
		}
	}

	// Write file
	if err := os.WriteFile(localPath, data, 0644); err != nil {
		fmt.Printf("Error writing file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Downloaded to %s (%d bytes)\n", localPath, len(data))
}

func handleList() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: lilio ls <bucket> [prefix]")
		fmt.Println("\nExamples:")
		fmt.Println("  lilio ls mybucket")
		fmt.Println("  lilio ls mybucket/photos/")
		os.Exit(1)
	}

	path := os.Args[2]

	listCmd := flag.NewFlagSet("ls", flag.ExitOnError)
	server := listCmd.String("server", defaultServer, "Server URL")
	jsonOutput := listCmd.Bool("json", false, "Output as JSON")
	listCmd.Parse(os.Args[3:])

	// Parse bucket/prefix
	parts := strings.SplitN(path, "/", 2)
	bucket := parts[0]
	prefix := ""
	if len(parts) > 1 {
		prefix = parts[1]
	}

	// List objects
	url := fmt.Sprintf("%s/%s", *server, bucket)

	if prefix != "" {
		url += "?prefix=" + prefix
	}
	fmt.Println(url)
	resp, err := http.Get(url)
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
		Bucket  string   `json:"bucket"`
		Objects []string `json:"objects"`
	}
	json.Unmarshal(body, &result)

	if len(result.Objects) == 0 {
		fmt.Printf("No objects in %s", bucket)
		if prefix != "" {
			fmt.Printf(" with prefix '%s'", prefix)
		}
		fmt.Println()
		return
	}

	fmt.Printf("Objects in %s:\n", bucket)
	for _, obj := range result.Objects {
		fmt.Printf("  %s\n", obj)
	}
	fmt.Printf("\nTotal: %d objects\n", len(result.Objects))
}

func handleDelete() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: lilio rm <bucket/key>")
		fmt.Println("\nExample:")
		fmt.Println("  lilio rm photos/vacation/photo.jpg")
		os.Exit(1)
	}

	remotePath := os.Args[2]

	rmCmd := flag.NewFlagSet("rm", flag.ExitOnError)
	server := rmCmd.String("server", defaultServer, "Server URL")
	rmCmd.Parse(os.Args[3:])

	// Parse bucket/key
	parts := strings.SplitN(remotePath, "/", 2)
	if len(parts) != 2 {
		fmt.Println("Error: path must be in format bucket/key")
		os.Exit(1)
	}
	bucket, key := parts[0], parts[1]

	// Delete
	url := fmt.Sprintf("%s/%s/%s", *server, bucket, key)
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

	fmt.Printf("✓ Deleted %s/%s\n", bucket, key)
}

func getContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	types := map[string]string{
		".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".png": "image/png", ".gif": "image/gif",
		".pdf": "application/pdf", ".txt": "text/plain",
		".html": "text/html", ".json": "application/json",
		".xml": "application/xml", ".zip": "application/zip",
		".mp3": "audio/mpeg", ".mp4": "video/mp4",
	}
	if ct, ok := types[ext]; ok {
		return ct
	}
	return "application/octet-stream"
}
