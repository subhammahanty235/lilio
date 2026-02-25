package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/subhammahanty235/lilio/pkg/storage"
)

type Server struct {
	lio  *storage.Lilio
	addr string
}

func NewServer(lio *storage.Lilio, addr string) *Server {
	return &Server{
		lio:  lio,
		addr: addr,
	}
}

func jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func errorResponse(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}

func parsePath(path string) (bucket, key string) {
	path = strings.TrimPrefix(path, "/")
	parts := strings.SplitN(path, "/", 2)

	if len(parts) > 0 {
		bucket = parts[0]
	}
	if len(parts) > 1 {
		key = parts[1]
	}
	return
}

// APIS
// ║  Server running at: http://%s
// ║                                                            ║
// ║  API Endpoints:                                            ║
// ║    GET    /                    - List buckets              ║
// ║    PUT    /{bucket}            - Create bucket             ║
// ║    DELETE /{bucket}            - Delete bucket             ║
// ║    GET    /{bucket}            - List objects              ║
// ║    PUT    /{bucket}/{key}      - Upload object             ║
// ║    GET    /{bucket}/{key}      - Download object           ║
// ║    DELETE /{bucket}/{key}      - Delete object             ║
// ║    HEAD   /{bucket}/{key}      - Get object metadata       ║
// ║    GET    /admin/stats         - Storage statistics        ║
// ║                                                            ║
// ║  Press Ctrl+C to stop

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.handleBucketsOrObjects(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		buckets, err := s.lio.ListBuckets()
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{"buckets": buckets})

	default:
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
	}

}

func (s *Server) handleBucketsOrObjects(w http.ResponseWriter, r *http.Request) {
	bucket, key := parsePath(r.URL.Path)
	if bucket == "admin" && key == "stats" {
		stats := s.lio.GetStorageStats()
		jsonResponse(w, http.StatusOK, stats)
		return
	}

	// NO KEY PROVIDED: bucket operations
	if key == "" {
		s.handleBucket(w, r, bucket)
		return
	}

	// KEY PROVIDED: Object Operations
	s.handleObject(w, r, bucket, key)
}

func (s *Server) handleBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodPut:
		if err := s.lio.CreateBucket(bucket); err != nil {
			errorResponse(w, http.StatusConflict, err.Error())
			return
		}

		jsonResponse(w, http.StatusCreated, map[string]string{
			"message": fmt.Sprintf("Bucket '%s' created", bucket),
		})

	// case get
	case http.MethodGet:
		// List objects in bucket
		prefix := r.URL.Query().Get("prefix")
		objects, err := s.lio.ListObjects(bucket, prefix)
		if err != nil {
			errorResponse(w, http.StatusNotFound, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"bucket":  bucket,
			"objects": objects,
		})
	case http.MethodDelete:
		// Delete bucket
		if err := s.lio.Metadata.DeleteBucket(bucket); err != nil {
			errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{
			"message": fmt.Sprintf("Bucket '%s' deleted", bucket),
		})

	default:
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
	}
	// case delete

}

func (s *Server) handleObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	switch r.Method {
	case http.MethodPut:
		data, err := io.ReadAll(r.Body)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "failed to read body")
			return
		}

		defer r.Body.Close()

		contentType := r.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		meta, err := s.lio.PutObject(bucket, key, data, contentType)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}

		jsonResponse(w, http.StatusCreated, map[string]interface{}{
			"message":  "Object stored",
			"key":      key,
			"size":     meta.Size,
			"checksum": meta.Checksum,
			"chunks":   meta.TotalChunks,
		})

	case http.MethodGet:
		data, err := s.lio.GetObject(bucket, key)
		if err != nil {
			errorResponse(w, http.StatusNotFound, err.Error())
			return
		}

		metadata, err := s.lio.HeadObject(bucket, key)
		contentType := "application/octet-stream"
		if metadata != nil {
			contentType = metadata.ContentType
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	}

}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)

	fmt.Printf(`
╔════════════════════════════════════════════════════════════╗
║              Mini S3 HTTP API Server (Go)                  ║
╠════════════════════════════════════════════════════════════╣
║  Server running at: http://%s                              ║
║                                                            ║
║  API Endpoints:                                            ║
║    GET    /                    - List buckets              ║
║    PUT    /{bucket}            - Create bucket             ║
║    DELETE /{bucket}            - Delete bucket             ║
║    GET    /{bucket}            - List objects              ║
║    PUT    /{bucket}/{key}      - Upload object             ║
║    GET    /{bucket}/{key}      - Download object           ║
║    DELETE /{bucket}/{key}      - Delete object             ║
║    HEAD   /{bucket}/{key}      - Get object metadata       ║
║    GET    /admin/stats         - Storage statistics        ║
║                                                            ║
║  Press Ctrl+C to stop                                      ║
╚════════════════════════════════════════════════════════════╝
`, s.addr)
	log.Printf("Starting server on %s", s.addr)
	return http.ListenAndServe(s.addr, mux)
}
