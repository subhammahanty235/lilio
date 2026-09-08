// Package chunkd implements the HTTP surface of a Lilio chunk server.
//
// A chunk server is deliberately dumb: it stores chunks, returns them, and
// deletes them. It knows nothing about objects, buckets, chunking, encryption,
// replication or placement - all of that stays in the coordinator. That is what
// makes it safe to run on a spare machine and simple to reason about.
package chunkd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	storagemodels "github.com/subhammahanty235/lilio/pkg/storage/storage-models"
)

// DefaultMaxChunkSize bounds what a single request can make a node allocate.
const DefaultMaxChunkSize int64 = 64 << 20

// New builds a chunk server over a local storage directory. Passing 0 for
// maxChunk uses DefaultMaxChunkSize.
func New(backend *storagemodels.LocalBackendPod, name string, maxChunk int64) *Server {
	if maxChunk <= 0 {
		maxChunk = DefaultMaxChunkSize
	}
	return &Server{backend: backend, name: name, maxChunk: maxChunk}
}

type Server struct {
	backend  *storagemodels.LocalBackendPod
	name     string
	maxChunk int64
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /stats", s.stats)
	mux.HandleFunc("GET /chunks", s.listChunks)
	mux.HandleFunc("PUT /chunks/{id}", s.putChunk)
	mux.HandleFunc("GET /chunks/{id}", s.getChunk)
	mux.HandleFunc("HEAD /chunks/{id}", s.headChunk)
	mux.HandleFunc("DELETE /chunks/{id}", s.deleteChunk)
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.backend.Health(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "node": s.name})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.backend.Stats(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) listChunks(w http.ResponseWriter, r *http.Request) {
	chunks, err := s.backend.ListChunks(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if chunks == nil {
		chunks = []string{}
	}
	writeJSON(w, http.StatusOK, map[string][]string{"chunks": chunks})
}

func (s *Server) putChunk(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	defer r.Body.Close()

	// Bound what a single request can make this node allocate.
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxChunk))
	if err != nil {
		http.Error(w, "chunk too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}

	if err := s.backend.StoreChunk(r.Context(), id, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) getChunk(w http.ResponseWriter, r *http.Request) {
	data, err := s.backend.RetrieveChunk(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (s *Server) headChunk(w http.ResponseWriter, r *http.Request) {
	if !s.backend.HasChunk(r.Context(), r.PathValue("id")) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) deleteChunk(w http.ResponseWriter, r *http.Request) {
	// LocalBackendPod already treats a missing chunk as success, which keeps
	// this endpoint idempotent: a client retrying after a timeout must not be
	// told the delete failed.
	if err := s.backend.DeleteChunk(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
