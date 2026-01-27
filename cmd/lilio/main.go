package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/subhammahanty235/lilio/pkg/api"
	"github.com/subhammahanty235/lilio/pkg/storage"
	storagemodels "github.com/subhammahanty235/lilio/pkg/storage/storage-models"
)

func main() {
	host := flag.String("host", "0.0.0.0", "Host to bind to")
	port := flag.Int("port", 8080, "Port to listen on")
	dataPath := flag.String("data", "./lilio_data", "Datastorage path")
	// nodes := flag.Int("nodes", 4, "number of storage nodes")
	chunkSize := flag.Int("Chunk-size", 512, "Chunk size in kb")
	replication := flag.Int("replication", 2, "Replication factor")

	flag.Parse()
	// config initialization
	cfg := storage.Config{
		BasePath:          *dataPath,
		ChunkSize:         *chunkSize * 1024,
		ReplicationFactor: *replication,
	}
	// create storage instance of the cfg
	lio, err := storage.NewLilioInstance(cfg)
	if err != nil {
		fmt.Printf("Failed to initialize storage: %v\n", err)
		os.Exit(1)
	}

	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("node_%d", i)
		nodePath := filepath.Join(cfg.BasePath, "storage_nodes", name)

		backend, err := storagemodels.NewLocalBackendPod(name, nodePath, i)
		if err != nil {
			panic(err)
		}
		lio.AddBackend(backend)
	}

	// server

	addr := fmt.Sprintf("%s:%d", *host, *port)
	server := api.NewServer(lio, addr)

	if err := server.Start(); err != nil {
		fmt.Printf("Server error: %v\n", err)
		os.Exit(1)
	}
}
