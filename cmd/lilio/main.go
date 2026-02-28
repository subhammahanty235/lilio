package main

import (
	"fmt"
	"os"
)

const VERSION = "0.2.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "server":
		runServer()
	case "web":
		handleWeb()
	case "storage":
		handleStorage()
	case "put":
		handlePut()
	case "get":
		handleGet()
	case "ls":
		handleList()
	case "rm":
		handleDelete()
	case "bucket":
		handleBucket()
	case "health":
		handleHealth()
	case "init":
		handleInit()
	case "version", "-v", "--version":
		fmt.Printf("lilio version %s\n", VERSION)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`
Lilio - Distributed Object Storage
===================================

Usage: lilio <command> [options]

Commands:
  init                        Initialize config file
  server                      Start the HTTP API server
  web                         Open web interface in browser

  storage add <type> [opts]   Add a storage backend
  storage remove <name>       Remove a storage backend
  storage list                List all storage backends
  
  bucket create <name>        Create a bucket
  bucket delete <name>        Delete a bucket
  bucket list                 List all buckets
  
  put <local> <remote>        Upload a file
  get <remote> <local>        Download a file
  ls <bucket> [prefix]        List objects in bucket
  rm <bucket>/<key>           Delete an object
  
  health                      Check health of all backends
  version                     Show version
  help                        Show this help

Storage Types:
  local     Local filesystem storage
  gdrive    Google Drive (coming soon)
  dropbox   Dropbox (coming soon)
  s3        S3-compatible storage (coming soon)

Examples:
  # Initialize config
  lilio init
  
  # Add local storage
  lilio storage add local --name disk1 --path /mnt/disk1
  lilio storage add local --name disk2 --path /mnt/disk2
  
  # List storage backends
  lilio storage list
  
  # Start server
  lilio server --port 8080
  
  # Create bucket and upload
  lilio bucket create photos
  lilio put ./photo.jpg photos/vacation/photo.jpg
  
  # Download and list
  lilio get photos/vacation/photo.jpg ./downloaded.jpg
  lilio ls photos
  
  # Check health
  lilio health

Config file: ./lilio.json (default)
`)
}
