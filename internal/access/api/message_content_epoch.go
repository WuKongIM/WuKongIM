package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// messageContentEpochMiddleware labels content only when its complete read did
// not cross a restore transition, including a delayed request body.
func (s *Server) messageContentEpochMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.contentEpoch == nil {
			return
		}
		switch c.Request.URL.Path {
		case "/message/update", "/channel/messageupdates", "/messages", "/channel/messagesync", "/channel/messagesyncbatch", "/conversation/list", "/conversation/sync":
		default:
			return
		}
		var fence uint64
		if s.contentReadFence != nil {
			var active bool
			fence, active = s.contentReadFence()
			if active {
				c.AbortWithStatusJSON(503, gin.H{"status": 503, "code": "unavailable", "msg": "temporarily unavailable"})
				return
			}
		}
		epoch, err := s.contentEpoch(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(503, gin.H{"status": 503, "code": "unavailable", "msg": "temporarily unavailable"})
			return
		}
		c.Header("X-WK-Content-Epoch", strconv.FormatUint(epoch, 10))
		guard := &contentEpochWriter{ResponseWriter: c.Writer, valid: func() bool {
			if s.contentReadFence != nil {
				current, active := s.contentReadFence()
				return !active && current == fence
			}
			// Embedding callers without the cheap node-local fence must resample before
			// publishing. Product wiring always supplies the transition fence.
			current, e := s.contentEpoch(c.Request.Context())
			return e == nil && current == epoch && (s.maintenance == nil || !s.maintenance())
		}}
		c.Writer = guard
		c.Next()
		guard.WriteHeaderNow()
	}
}

// contentEpochWriter checks before the first response byte, then streams through
// the existing writer. It adds no response-body buffer or duplicate JSON copy.
type contentEpochWriter struct {
	gin.ResponseWriter
	valid    func() bool
	checked  bool
	rejected bool
}

func (w *contentEpochWriter) check() bool {
	if w.checked {
		return !w.rejected
	}
	w.checked = true
	if w.Status() < 200 || w.Status() >= 300 || w.valid() {
		return true
	}
	w.rejected = true
	w.Header().Del("X-WK-Content-Epoch")
	w.Header().Del("Content-Length")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.ResponseWriter.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.ResponseWriter.Write([]byte(`{"status":503,"code":"unavailable","msg":"content changed during restore"}`))
	return false
}
func (w *contentEpochWriter) Write(p []byte) (int, error) {
	if !w.check() {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}
func (w *contentEpochWriter) WriteString(p string) (int, error) {
	if !w.check() {
		return len(p), nil
	}
	return w.ResponseWriter.WriteString(p)
}
func (w *contentEpochWriter) WriteHeaderNow() {
	if w.check() {
		w.ResponseWriter.WriteHeaderNow()
	}
}
func (w *contentEpochWriter) Flush() {
	if w.check() {
		w.ResponseWriter.Flush()
	}
}
