package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/subhammahanty235/lilio/pkg/config"
)

func handleStorage() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: lilio storage <add|remove|list> [options]")
		fmt.Println("\nCommands:")
		fmt.Println("  add <type>     Add a new storage backend")
		fmt.Println("  remove <n>  Remove a storage backend")
		fmt.Println("  list           List all storage backends")
		os.Exit(1)
	}

	subcommand := os.Args[2]

	switch subcommand {
	case "add":
		storageAdd()
	case "remove", "rm":
		storageRemove()
	case "list", "ls":
		storageList()
	default:
		fmt.Printf("Unknown storage command: %s\n", subcommand)
		os.Exit(1)
	}
}

func storageAdd() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: lilio storage add <type> [options]")
		fmt.Println("\nTypes:")
		fmt.Println("  local     Local filesystem")
		fmt.Println("  gdrive    Google Drive (coming soon)")
		fmt.Println("  dropbox   Dropbox (coming soon)")
		fmt.Println("  s3        S3-compatible (coming soon)")
		fmt.Println("\nExamples:")
		fmt.Println("  lilio storage add local --name disk1 --path /mnt/disk1")
		fmt.Println("  lilio storage add local --name disk2 --path /mnt/disk2 --priority 2")
		os.Exit(1)
	}

	storageType := os.Args[3]

	addCmd := flag.NewFlagSet("storage add", flag.ExitOnError)
	name := addCmd.String("name", "", "Backend name (required)")
	configPath := addCmd.String("config", "./lilio.json", "Config file path")
	priority := addCmd.Int("priority", 10, "Priority (lower = preferred)")

	// Type-specific options
	path := addCmd.String("path", "", "Path for local storage")
	credentials := addCmd.String("credentials", "", "Credentials file for cloud storage")
	token := addCmd.String("token", "", "API token")
	endpoint := addCmd.String("endpoint", "", "S3 endpoint URL")
	bucket := addCmd.String("bucket", "", "S3 bucket name")

	addCmd.Parse(os.Args[4:])

	if *name == "" {
		fmt.Println("Error: --name is required")
		os.Exit(1)
	}

	// Validate type-specific requirements
	options := make(map[string]string)

	switch storageType {
	case "local":
		if *path == "" {
			fmt.Println("Error: --path is required for local storage")
			os.Exit(1)
		}
		options["path"] = *path

	case "gdrive":
		if *credentials == "" {
			fmt.Println("Error: --credentials is required for gdrive")
			os.Exit(1)
		}
		options["credentials"] = *credentials

	case "dropbox":
		if *token == "" {
			fmt.Println("Error: --token is required for dropbox")
			os.Exit(1)
		}
		options["token"] = *token

	case "s3":
		if *endpoint == "" || *bucket == "" {
			fmt.Println("Error: --endpoint and --bucket are required for s3")
			os.Exit(1)
		}
		options["endpoint"] = *endpoint
		options["bucket"] = *bucket

	default:
		fmt.Printf("Error: unknown storage type: %s\n", storageType)
		fmt.Println("Valid types: local, gdrive, dropbox, s3")
		os.Exit(1)
	}

	// Load or create config
	cfg, err := config.LoadOrCreate(*configPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Add storage
	storage := config.StorageConfig{
		Name:     *name,
		Type:     storageType,
		Priority: *priority,
		Options:  options,
	}

	if err := cfg.AddStorage(storage); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Save config
	if err := cfg.Save(*configPath); err != nil {
		fmt.Printf("Error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Added storage backend: %s (%s)\n", *name, storageType)
	fmt.Printf("  Path: %s\n", *path)
	fmt.Printf("  Priority: %d\n", *priority)
	fmt.Printf("\nConfig saved to: %s\n", *configPath)
	fmt.Println("Restart server to apply changes")
}

func storageRemove() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: lilio storage remove <n>")
		os.Exit(1)
	}

	name := os.Args[3]

	removeCmd := flag.NewFlagSet("storage remove", flag.ExitOnError)
	configPath := removeCmd.String("config", "./lilio.json", "Config file path")
	removeCmd.Parse(os.Args[4:])

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Remove storage
	if err := cfg.RemoveStorage(name); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Save config
	if err := cfg.Save(*configPath); err != nil {
		fmt.Printf("Error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Removed storage backend: %s\n", name)
	fmt.Println("Restart server to apply changes")
}

func storageList() {
	listCmd := flag.NewFlagSet("storage list", flag.ExitOnError)
	configPath := listCmd.String("config", "./lilio.json", "Config file path")
	jsonOutput := listCmd.Bool("json", false, "Output as JSON")
	listCmd.Parse(os.Args[3:])

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		fmt.Println("\nRun 'lilio init' to create a config file")
		os.Exit(1)
	}

	if *jsonOutput {
		data, _ := json.MarshalIndent(cfg.Storages, "", "  ")
		fmt.Println(string(data))
		return
	}

	if len(cfg.Storages) == 0 {
		fmt.Println("No storage backends configured")
		fmt.Println("\nAdd storage with:")
		fmt.Println("  lilio storage add local --name disk1 --path /mnt/disk1")
		return
	}

	// Print table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTYPE\tPRIORITY\tPATH/OPTIONS")
	fmt.Fprintln(w, "----\t----\t--------\t------------")

	for _, s := range cfg.Storages {
		pathOrOpt := ""
		if p, ok := s.Options["path"]; ok {
			pathOrOpt = p
		} else if c, ok := s.Options["credentials"]; ok {
			pathOrOpt = "creds: " + c
		} else if e, ok := s.Options["endpoint"]; ok {
			pathOrOpt = e
		}

		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", s.Name, s.Type, s.Priority, pathOrOpt)
	}
	w.Flush()

	fmt.Printf("\nTotal: %d backends\n", len(cfg.Storages))
}
