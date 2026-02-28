package web

import (
	_ "embed"
	"net/http"
)

//go:embed assets/index.html
var indexHTML []byte

// ServeUI serves the web UI for all /ui routes
func ServeUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(http.StatusOK)
	w.Write(indexHTML)
}
