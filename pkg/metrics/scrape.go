package metrics

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Scrapes favor CPU efficiency over maximum compression. Each active response
// owns one writer; pooling detaches its HTTP response, and no full body is buffered.
var scrapeGzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return w
}}

// metricsScrapeHandler keeps promhttp's gathering and format/error handling,
// replacing only its default gzip compression level for successful scrapes.
func metricsScrapeHandler(gatherer prometheus.Gatherer) http.Handler {
	handler := promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{DisableCompression: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !scrapeGzipAccepted(r.Header) {
			handler.ServeHTTP(w, r)
			return
		}
		rsp := &gzipScrapeResponse{ResponseWriter: w}
		defer func() {
			if rsp.gzip != nil {
				// The upstream handler also leaves final transport errors to the
				// HTTP server. Always detach the response before pooling the writer.
				_ = rsp.gzip.Close()
				rsp.gzip.Reset(io.Discard)
				scrapeGzipPool.Put(rsp.gzip)
			}
		}()
		handler.ServeHTTP(rsp, r)
		if !rsp.wroteHeader {
			// An empty successful exposition still needs a complete gzip stream.
			rsp.WriteHeader(http.StatusOK)
		}
	})
}

// Match the pinned promhttp negotiation, including parameterized gzip entries.
func scrapeGzipAccepted(header http.Header) bool {
	for _, part := range strings.Split(header.Get("Accept-Encoding"), ",") {
		part = strings.TrimSpace(part)
		if part == "gzip" || strings.HasPrefix(part, "gzip;") {
			return true
		}
	}
	return false
}

// gzipScrapeResponse streams a successful exposition without buffering its body.
type gzipScrapeResponse struct {
	http.ResponseWriter
	// gzip is acquired only when a successful response starts. Gather failures
	// retain promhttp's uncompressed 500 response and never enter the pool.
	gzip        *gzip.Writer
	wroteHeader bool
}

func (w *gzipScrapeResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// WriteHeader starts compression only for successful scrapes, preserving the
// upstream handler's plain-text error status and body.
func (w *gzipScrapeResponse) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if status == http.StatusOK {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
		w.gzip = scrapeGzipPool.Get().(*gzip.Writer)
		w.gzip.Reset(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(status)
}

// Write sends each encoded metric directly through this response's writer.
func (w *gzipScrapeResponse) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.gzip != nil {
		return w.gzip.Write(p)
	}
	return w.ResponseWriter.Write(p)
}
