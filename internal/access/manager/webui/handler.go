package webui

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
)

var embeddedHandler = newEmbeddedHandler()

// Handler returns the read-only HTTP handler for the embedded manager web app.
func Handler() http.Handler {
	return embeddedHandler
}

func newEmbeddedHandler() http.Handler {
	dist, err := fs.Sub(embeddedDist, "dist")
	if err != nil {
		panic("manager web bundle: " + err.Error())
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		panic("manager web index: " + err.Error())
	}
	return &handler{
		assets:     dist,
		fileServer: http.FileServer(http.FS(dist)),
		index:      index,
		indexETag:  fmt.Sprintf("\"%x\"", sha256.Sum256(index)),
	}
}

// handler serves immutable build assets and falls back to the SPA entry point.
type handler struct {
	// assets contains the embedded Vite distribution.
	assets fs.FS
	// fileServer serves exact static asset requests from assets.
	fileServer http.Handler
	// index is the cached SPA entry point used for history routing.
	index []byte
	// indexETag is the stable content fingerprint for conditional requests.
	indexETag string
}

// ServeHTTP gives manager API paths precedence, serves exact assets, and falls
// back to the SPA entry point for browser history routes.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/manager" || strings.HasPrefix(r.URL.Path, "/manager/") {
		http.NotFound(w, r)
		return
	}
	assetName := strings.TrimPrefix(r.URL.Path, "/")
	if isStaticAssetPath(assetName) {
		info, err := fs.Stat(h.assets, assetName)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(assetName, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		h.fileServer.ServeHTTP(w, r)
		return
	}

	h.serveIndex(w, r)
}

func isStaticAssetPath(name string) bool {
	return name == "assets" || strings.HasPrefix(name, "assets/") || path.Ext(name) != ""
}

// serveIndex serves the cached entry point and handles ETag revalidation.
func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("ETag", h.indexETag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if ifNoneMatch := r.Header.Get("If-None-Match"); ifNoneMatch == h.indexETag || ifNoneMatch == "*" {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(h.index)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(h.index)
}
