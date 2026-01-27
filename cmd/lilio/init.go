package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/subhammahanty235/lilio/pkg/config"
)

func handleInit() {
	initCmd := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := initCmd.String("config", "./lilio.json", "Config file path")
	force := initCmd.Bool("force", false, "Overwrite existing config")

	initCmd.Parse(os.Args[2:])

	// Check if config already exists
	if _, err := os.Stat(*configPath); err == nil && !*force {
		fmt.Printf("Config file already exists: %s\n", *configPath)
		fmt.Println("Use --force to overwrite")
		os.Exit(1)
	}

	// Create default config
	cfg := config.DefaultConfig()

	// Add some default local storage
	cfg.Storages = []config.StorageConfig{
		{
			Name:     "local-1",
			Type:     "local",
			Priority: 1,
			Options: map[string]string{
				"path": "./lilio_data/storage/local-1",
			},
		},
		{
			Name:     "local-2",
			Type:     "local",
			Priority: 2,
			Options: map[string]string{
				"path": "./lilio_data/storage/local-2",
			},
		},
	}

	if err := cfg.Save(*configPath); err != nil {
		fmt.Printf("Error creating config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Created config file: %s\n", *configPath)
	fmt.Println("\nDefault storage backends added:")
	for _, s := range cfg.Storages {
		fmt.Printf("  - %s (%s): %s\n", s.Name, s.Type, s.Options["path"])
	}
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Edit lilio.json to add more storage backends")
	fmt.Println("  2. Run: lilio server")
	fmt.Println("  3. Or add storage: lilio storage add local --name disk3 --path /mnt/disk3")
}
