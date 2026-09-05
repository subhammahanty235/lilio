package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/subhammahanty235/lilio/pkg/api"
	"github.com/subhammahanty235/lilio/pkg/config"
	"github.com/subhammahanty235/lilio/pkg/metadata"
	"github.com/subhammahanty235/lilio/pkg/metrics"
	"github.com/subhammahanty235/lilio/pkg/storage"
	storagemodels "github.com/subhammahanty235/lilio/pkg/storage/storage-models"
)

func runServer() {
	serverCmd := flag.NewFlagSet("server", flag.ExitOnError)
	port := serverCmd.Int("port", 8080, "port to listen on")
	host := serverCmd.String("host", "0.0.0.0", "Host to bind to")
	configPath := serverCmd.String("config", "./lilio.json", "Config file path")
	dataPath := serverCmd.String("data", "./lilio_data", "Data directory (if no config)")

	serverCmd.Parse(os.Args[2:])

	var lio *storage.Lilio
	// var err error

	// Try to load config file
	if _, err := os.Stat(*configPath); err == nil {
		fmt.Printf("Loading config from: %s\n", *configPath)
		lio, err = initFromConfig(*configPath)
		if err != nil {
			fmt.Printf("Failed to load config: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Use default config with command line options
		fmt.Println("No config file found, using defaults...")
		fmt.Println("Run 'lilio init' to create a config file")

		cfg := storage.Config{
			BasePath:          *dataPath,
			ChunkSize:         1024 * 1024,
			ReplicationFactor: 2,
		}

		lio, err = storage.NewLilioInstance(cfg)
		if err != nil {
			fmt.Printf("Failed to initialize: %v\n", err)
			os.Exit(1)
		}

		// Create default backends
		for i := 0; i < 4; i++ {
			name := fmt.Sprintf("node_%d", i)
			nodePath := filepath.Join(*dataPath, "storage_nodes", name)

			backend, err := storagemodels.NewLocalBackendPod(name, nodePath, i)
			if err != nil {
				fmt.Printf("Warning: failed to create backend %s: %v\n", name, err)
				continue
			}
			lio.AddBackend(backend)
		}
	}

	// Start server
	addr := fmt.Sprintf("%s:%d", *host, *port)
	server := api.NewServer(lio, addr)

	if err := server.Start(); err != nil {
		fmt.Printf("Server error: %v\n", err)
		os.Exit(1)
	}
}

// initFromConfig initializes Lilio from a config file
func initFromConfig(configPath string) (*storage.Lilio, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	// Parse chunk size
	chunkSize, err := config.ParseChunkSize(cfg.Lilio.ChunkSize)
	if err != nil {
		return nil, fmt.Errorf("invalid chunk size: %w", err)
	}

	metadataCfg, err := metadataConfig(cfg)
	if err != nil {
		return nil, err
	}

	// Create Lilio instance
	storageCfg := storage.Config{
		BasePath:          cfg.Lilio.MetadataPath,
		ChunkSize:         chunkSize,
		ReplicationFactor: cfg.Lilio.ReplicationFactor,
		MetadataConfig:    &metadataCfg,
		MetricsConfig:     metricsConfig(cfg),
		Quorum:            quorumConfig(cfg),
	}

	lio, err := storage.NewLilioInstance(storageCfg)
	if err != nil {
		return nil, err
	}

	// Add backends from config
	for _, storageCfg := range cfg.Storages {
		backend, err := createBackend(storageCfg)
		if err != nil {
			fmt.Printf("  ⚠ Skipping %s: %v\n", storageCfg.Name, err)
			// Track the failed backend
			lio.TrackFailedBackend(storageCfg.Name, storageCfg.Type, storageCfg.Priority, err)
			continue
		}

		if err := lio.AddBackend(backend); err != nil {
			fmt.Printf("  ⚠ Failed to add %s: %v\n", storageCfg.Name, err)
			// Track the failed backend
			lio.TrackFailedBackend(storageCfg.Name, storageCfg.Type, storageCfg.Priority, err)
			continue
		}

		fmt.Printf("  ✓ Added backend: %s (%s)\n", storageCfg.Name, storageCfg.Type)
	}

	// Allow server to start even if all backends failed (show them in dashboard)
	totalBackends := lio.Registry.Count() + len(lio.FailedBackends)
	if totalBackends == 0 {
		return nil, fmt.Errorf("no storage backends configured")
	}

	if lio.Registry.Count() == 0 {
		fmt.Println("  ⚠ Warning: No healthy backends available. Server running in degraded mode.")
	}

	return lio, nil
}

// createBackend creates a storage backend from config
func createBackend(cfg config.StorageConfig) (storage.StorageBackend, error) {
	switch cfg.Type {
	case "local":
		path := cfg.GetOption("path", "./lilio_data/storage/"+cfg.Name)
		return storagemodels.NewLocalBackendPod(cfg.Name, path, cfg.Priority)

	case "gdrive":
		fmt.Printf("DEBUG: cfg.Options = %+v\n", cfg.Options)
		credentials := cfg.GetOption("credentials", "")
		fmt.Println(credentials)
		if credentials == "" {
			return nil, fmt.Errorf("gdrive requires 'credentials' option (path to credentials.json)")
		}

		tokenPath := cfg.GetOption("token_path", "./lilio_data/tokens/"+cfg.Name+"_token.json")
		folderID := cfg.GetOption("folder_id", "")
		return storagemodels.NewGDriveBackend(cfg.Name, credentials, tokenPath, folderID, cfg.Priority)
		// return nil, fmt.Errorf("gdrive backend coming soon")

	case "dropbox":
		return nil, fmt.Errorf("dropbox backend coming soon")

	case "s3":
		return nil, fmt.Errorf("s3 backend coming soon")

	default:
		return nil, fmt.Errorf("unknown backend type: %s", cfg.Type)
	}

}

// metadataConfig translates the file's metadata section into the storage core's
// configuration.
//
// It is always returned non-nil, which also settles where local metadata lives:
// the core appends "/metadata" to BasePath when no metadata config is supplied,
// so a metadata_path of "./lilio_data/metadata" used to land the files in
// "./lilio_data/metadata/metadata".
func metadataConfig(cfg *config.Config) (metadata.Config, error) {
	path := cfg.Lilio.MetadataPath
	if path == "" {
		path = "./lilio_data/metadata"
	}

	m := cfg.Metadata
	if m == nil || m.Type == "" || m.Type == "local" {
		if m != nil && m.Local != nil && m.Local.Path != "" {
			path = m.Local.Path
		}
		return metadata.Config{
			Type:        metadata.StoreTypeLocal,
			LocalConfig: &metadata.LocalConfig{Path: path},
		}, nil
	}

	switch m.Type {
	case "memory":
		return metadata.Config{Type: metadata.StoreTypeMemory}, nil

	case "etcd":
		timeout, err := m.Etcd.DialTimeoutDuration()
		if err != nil {
			return metadata.Config{}, fmt.Errorf("invalid metadata etcd.dial_timeout: %w", err)
		}
		return metadata.Config{
			Type: metadata.StoreTypeEtcd,
			EtcdConfig: &metadata.EtcdConfig{
				Endpoints:   m.Etcd.Endpoints,
				Prefix:      m.Etcd.Prefix,
				DialTimeout: timeout,
				Username:    m.Etcd.Username,
				Password:    m.Etcd.Password,
			},
		}, nil

	default:
		return metadata.Config{}, fmt.Errorf("unknown metadata type: %s", m.Type)
	}
}

// metricsConfig translates the file's metrics section. Returning nil leaves the
// core on its default, which is Prometheus enabled.
func metricsConfig(cfg *config.Config) *metrics.Config {
	if cfg.Metrics == nil {
		return nil
	}
	return &metrics.Config{
		Enabled: cfg.Metrics.Enabled,
		Type:    metrics.MetricType(cfg.Metrics.Type),
		Path:    cfg.Metrics.Path,
	}
}

// quorumConfig translates the file's quorum section. Returning nil leaves the
// core to derive a majority W and R from the replication factor.
func quorumConfig(cfg *config.Config) *storage.QuorumConfig {
	q := cfg.Lilio.Quorum
	if q == nil {
		return nil
	}
	return &storage.QuorumConfig{
		N: q.EffectiveN(cfg.Lilio.ReplicationFactor),
		W: q.W,
		R: q.R,
	}
}
