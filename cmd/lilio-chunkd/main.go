// Command lilio-chunkd is a chunk server: the daemon that turns a disk on some
// other machine into a Lilio storage node.
//
// Usage:
//
//	lilio-chunkd --name node-1 --data /mnt/hdd/lilio --port 9000
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/subhammahanty235/lilio/pkg/chunkd"
	storagemodels "github.com/subhammahanty235/lilio/pkg/storage/storage-models"
)

const version = "0.1.0"

func main() {
	var (
		name     = flag.String("name", "chunkd", "Node name, for logs and stats")
		dataPath = flag.String("data", "./chunkd_data", "Directory chunks are stored in")
		addr     = flag.String("addr", "0.0.0.0", "Address to bind to")
		port     = flag.Int("port", 9000, "Port to listen on")
		maxChunk = flag.Int64("max-chunk-size", chunkd.DefaultMaxChunkSize, "Largest chunk accepted, in bytes")
	)
	flag.Parse()

	// A chunk server stores chunks exactly the way the coordinator's local
	// backend does, so this is that same type with HTTP in front of it.
	backend, err := storagemodels.NewLocalBackendPod(*name, *dataPath, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open data directory: %v\n", err)
		os.Exit(1)
	}

	listen := fmt.Sprintf("%s:%d", *addr, *port)
	log.Printf("lilio-chunkd %s | node=%s data=%s listening on %s", version, *name, *dataPath, listen)
	log.Printf("WARNING: this endpoint is unauthenticated. Bind it to a private network only.")

	srv := &http.Server{
		Addr:              listen,
		Handler:           chunkd.New(backend, *name, *maxChunk).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
