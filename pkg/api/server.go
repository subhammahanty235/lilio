package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/subhammahanty235/lilio/pkg/metadata"
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

// writeObjectError maps a storage error onto an HTTP status.
//
// Telling absence apart from failure matters to the client: a 404 means the
// object is not there and the request should not be repeated, a 500 means
// something broke and a retry may well succeed. This handler previously
// returned 404 for every error, including an unreachable metadata backend.
func writeObjectError(w http.ResponseWriter, err error) {
	// Discard any object headers staged before the failure became known.
	w.Header().Del("Content-Length")
	w.Header().Del("ETag")
	w.Header().Del("Last-Modified")

	switch {
	case errors.Is(err, metadata.ErrObjectNotFound), errors.Is(err, metadata.ErrBucketNotFound):
		errorResponse(w, http.StatusNotFound, err.Error())
	default:
		errorResponse(w, http.StatusInternalServerError, err.Error())
	}
}

// setObjectHeaders stages the response headers describing an object. They are
// not sent until something calls WriteHeader, which lets a caller stage them
// and still change its mind.
func setObjectHeaders(w http.ResponseWriter, meta *metadata.ObjectMetadata) {
	contentType := meta.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	w.Header().Set("ETag", `"`+meta.Checksum+`"`)
	w.Header().Set("Last-Modified", meta.CreatedAt.UTC().Format(http.TimeFormat))
}

// deferredWriter holds back the response status line until the handler actually
// produces a byte of body.
//
// A streaming read cannot know it will succeed before it starts: an object's
// metadata can be perfectly readable while its chunks are not. Sending the
// status eagerly makes that failure unreportable, which is how a read that
// recovered nothing could still answer "200 OK" with an empty body. Deferring
// the status keeps a real error code available for as long as nothing has been
// sent, and makes it explicit at the point of failure that the choice is gone.
type deferredWriter struct {
	w       http.ResponseWriter
	status  int
	n       int64
	written bool
}

func (d *deferredWriter) Write(p []byte) (int, error) {
	d.commit()
	n, err := d.w.Write(p)
	d.n += int64(n)
	return n, err
}

// commit sends the status line if it has not gone out already.
func (d *deferredWriter) commit() {
	if !d.written {
		d.written = true
		d.w.WriteHeader(d.status)
	}
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
		stats := s.lio.GetStorageStats(r.Context())
		jsonResponse(w, http.StatusOK, stats)
		return
	}

	if bucket == "admin" && key == "health" {
		healthErrors := s.lio.HealthCheck(r.Context())

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
		defer r.Body.Close()

		contentType := r.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		meta, err := s.lio.PutObject(r.Context(), bucket, key, r.Body, r.ContentLength, contentType)
		if err != nil {
			writeObjectError(w, err)
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
		meta, err := s.lio.HeadObject(bucket, key)
		if err != nil {
			writeObjectError(w, err)
			return
		}
		setObjectHeaders(w, meta)

		body := &deferredWriter{w: w, status: http.StatusOK}
		if err := s.lio.GetObject(r.Context(), bucket, key, body); err != nil {
			if !body.written {
				// Nothing has reached the client yet, so the failure can still
				// be reported as a status code.
				writeObjectError(w, err)
				return
			}
			// The status line is already on the wire and cannot be withdrawn.
			// Breaking the connection is the only honest signal left: the
			// client then sees a failed transfer rather than a truncated body
			// that looks like a complete one. ErrAbortHandler aborts without
			// logging a panic trace.
			log.Printf("Error streaming object %s/%s after %d of %d bytes: %v",
				bucket, key, body.n, meta.Size, err)
			panic(http.ErrAbortHandler)
		}
		// A zero-byte object never triggers a Write, so it still needs a status.
		body.commit()

	case http.MethodHead:
		meta, err := s.lio.HeadObject(bucket, key)
		if err != nil {
			writeObjectError(w, err)
			return
		}
		// net/http discards any body written in response to a HEAD, so only
		// these headers reach the client. No storage backend is touched.
		setObjectHeaders(w, meta)
		w.WriteHeader(http.StatusOK)

	case http.MethodDelete:
		if err := s.lio.DeleteObject(r.Context(), bucket, key); err != nil {
			writeObjectError(w, err)
			return
		}
		// 204: succeeded, nothing to return. Deleting an object that was
		// already gone also reports success - see Lilio.DeleteObject on why
		// DELETE has to be idempotent.
		w.WriteHeader(http.StatusNoContent)

	default:
		// Without this, an unhandled method fell through the switch and
		// net/http answered 200 with an empty body - which is how DELETE
		// appeared to succeed while doing nothing at all.
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// Handler builds the server's HTTP routing. Start serves it; tests exercise it
// directly without binding a port.
func (s *Server) Handler() http.Handler {
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

	return mux
}

func (s *Server) Start() error {
	handler := s.Handler()

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
	return http.ListenAndServe(s.addr, handler)
}
