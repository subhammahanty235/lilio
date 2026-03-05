package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/subhammahanty235/lilio/pkg/storage"
	"github.com/subhammahanty235/lilio/pkg/web"
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
		// Check if detailed info is requested
		if r.URL.Query().Get("details") == "true" {
			s.handleListBucketsDetailed(w, r)
			return
		}

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

func (s *Server) handleListBucketsDetailed(w http.ResponseWriter, r *http.Request) {
	bucketNames, err := s.lio.ListBuckets()
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	type BucketInfo struct {
		Name      string `json:"name"`
		Encrypted bool   `json:"encrypted"`
		CreatedAt string `json:"created_at"`
	}

	var bucketsInfo []BucketInfo
	for _, name := range bucketNames {
		bucketMeta, err := s.lio.Metadata.GetBucket(name)
		if err != nil {
			// If we can't get metadata, just add basic info
			bucketsInfo = append(bucketsInfo, BucketInfo{
				Name:      name,
				Encrypted: false,
			})
			continue
		}

		bucketsInfo = append(bucketsInfo, BucketInfo{
			Name:      name,
			Encrypted: bucketMeta.Encryption.Enabled,
			CreatedAt: bucketMeta.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{"buckets": bucketsInfo})
}

func (s *Server) handleBucketsOrObjects(w http.ResponseWriter, r *http.Request) {
	bucket, key := parsePath(r.URL.Path)
	if bucket == "admin" && key == "stats" {
		stats := s.lio.GetStorageStats()
		jsonResponse(w, http.StatusOK, stats)
		return
	}

	if bucket == "admin" && key == "health" {
		healthErrors := s.lio.HealthCheck()

		// Convert to a more user-friendly format
		healthStatus := make(map[string]interface{})
		backends := s.lio.ListBackends()

		// Add healthy/online backends
		for _, info := range backends {
			status := map[string]interface{}{
				"name":     info.Name,
				"type":     info.Type,
				"status":   info.Status,
				"priority": info.Priority,
				"healthy":  true,
				"error":    nil,
			}

			if err, exists := healthErrors[info.Name]; exists {
				status["healthy"] = false
				status["error"] = err.Error()
				status["status"] = "offline"
			}

			healthStatus[info.Name] = status
		}

		// Add failed backends
		failedBackends := s.lio.GetFailedBackends()
		for name, failed := range failedBackends {
			healthStatus[name] = map[string]interface{}{
				"name":     failed.Name,
				"type":     failed.Type,
				"status":   "offline",
				"priority": failed.Priority,
				"healthy":  false,
				"error":    failed.Error,
			}
		}

		jsonResponse(w, http.StatusOK, healthStatus)
		return
	}

	// Handle unlock endpoint
	if key == "unlock" && r.Method == http.MethodPost {
		s.handleUnlock(w, r, bucket)
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
		// Check if encryption is requested
		encryption := r.URL.Query().Get("encryption")
		password := r.URL.Query().Get("password")

		var err error
		if encryption == "aes256" && password != "" {
			err = s.lio.CreateBucketWithEncryption(bucket, password)
		} else {
			err = s.lio.CreateBucket(bucket)
		}

		if err != nil {
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

func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request, bucket string) {
	password := r.URL.Query().Get("password")
	if password == "" {
		errorResponse(w, http.StatusBadRequest, "password required")
		return
	}

	if err := s.lio.UnlockBucket(bucket, password); err != nil {
		errorResponse(w, http.StatusUnauthorized, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{
		"message": fmt.Sprintf("Bucket '%s' unlocked", bucket),
	})
}

func (s *Server) handleObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	switch r.Method {
	case http.MethodPut:
		contentType := r.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		meta, err := s.lio.PutObject(bucket, key, r.Body, r.ContentLength, contentType)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer r.Body.Close()

		jsonResponse(w, http.StatusCreated, map[string]interface{}{
			"message":  "Object stored",
			"key":      key,
			"size":     meta.Size,
			"checksum": meta.Checksum,
			"chunks":   meta.TotalChunks,
		})

	case http.MethodGet:
		metadata, err := s.lio.HeadObject(bucket, key)
		if err != nil {
			errorResponse(w, http.StatusNotFound, err.Error())
			return
		}

		contentType := "application/octet-stream"
		if metadata != nil && metadata.ContentType != "" {
			contentType = metadata.ContentType
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", metadata.Size))
		w.WriteHeader(http.StatusOK)

		// Stream directly to response writer
		if err := s.lio.GetObject(bucket, key, w); err != nil {
			// Can't send error response here, headers already sent
			log.Printf("Error streaming object: %v", err)
		}
	}

}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/ui", web.ServeUI)
	mux.HandleFunc("/ui/", web.ServeUI)

	// Add metrics endpoint
	if metricsHandler := s.lio.Metrics.Handler(); metricsHandler != nil {
		if handler, ok := metricsHandler.(http.Handler); ok {
			mux.Handle("/metrics", handler)
		}
	}

	fmt.Printf(`
╔════════════════════════════════════════════════════════════╗
║              Mini S3 HTTP API Server (Go)                  ║
╠════════════════════════════════════════════════════════════╣
║  Server running at: http://%s                              ║
║                                                            ║
║  Web Interface:                                            ║
║    http://%s/ui                - Web UI                    ║
║                                                            ║
║  Metrics (%s):                                             ║
║    http://%s/metrics           - Prometheus metrics        ║
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
║    POST   /{bucket}/unlock     - Unlock encrypted bucket   ║
║    GET    /admin/stats         - Storage statistics        ║
║    GET    /admin/health        - Backend health status     ║
║                                                            ║
║  Press Ctrl+C to stop                                      ║
╚════════════════════════════════════════════════════════════╝
`, s.addr, s.addr, s.lio.Metrics.Type(), s.addr)
	log.Printf("Starting server on %s", s.addr)
	return http.ListenAndServe(s.addr, mux)
}
