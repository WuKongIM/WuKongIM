package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMessageContentEpochProtectsBothConversationResponses(t *testing.T) {
	for _, path := range []string{"/conversation/list", "/conversation/sync"} {
		t.Run(path, func(t *testing.T) {
			var epochErr error
			s := &Server{contentEpoch: func(context.Context) (uint64, error) { return 9007199254740993, epochErr }}
			r := gin.New()
			r.Use(s.messageContentEpochMiddleware())
			calls := 0
			r.POST(path, func(c *gin.Context) { calls++; c.JSON(http.StatusOK, []string{}) })
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
			if rec.Code != http.StatusOK || rec.Header().Get("X-WK-Content-Epoch") != "9007199254740993" || calls != 1 {
				t.Fatalf("status=%d headers=%v calls=%d", rec.Code, rec.Header(), calls)
			}
			epochErr = errors.New("controller unavailable")
			rec = httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
			if rec.Code != http.StatusServiceUnavailable || calls != 1 || rec.Header().Get("X-WK-Content-Epoch") != "" {
				t.Fatalf("unavailable epoch must fence content: status=%d headers=%v calls=%d", rec.Code, rec.Header(), calls)
			}
		})
	}
}

// restoreDuringBody simulates a request admitted before restore whose body only
// becomes available after the new content generation is published.
type restoreDuringBody struct {
	read    bool
	restore func()
}

func (r *restoreDuringBody) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	r.restore()
	return copy(p, `{"uid":"u"}`), nil
}
func TestContentEpochRejectsRestoreDuringRequestBody(t *testing.T) {
	var epoch uint64 = 7
	s := &Server{contentEpoch: func(context.Context) (uint64, error) { return epoch, nil }}
	r := gin.New()
	r.Use(s.messageContentEpochMiddleware())
	r.POST("/conversation/list", func(c *gin.Context) {
		var body map[string]string
		if err := c.ShouldBindJSON(&body); err != nil {
			t.Fatal(err)
		}
		c.JSON(200, gin.H{"payload": "restored"})
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/list", &restoreDuringBody{restore: func() { epoch = 8 }})
	r.ServeHTTP(rec, req)
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "restored") || rec.Header().Get("X-WK-Content-Epoch") != "" {
		t.Fatalf("mixed generation escaped: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
}

func TestContentEpochLocalFenceRejectsCompletedAndActiveRestore(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			stamp := uint64(0)
			restoring := false
			epochCalls := 0
			s := &Server{contentEpoch: func(context.Context) (uint64, error) { epochCalls++; return 7, nil }, contentReadFence: func() (uint64, bool) { return stamp, restoring }}
			r := gin.New()
			r.Use(s.messageContentEpochMiddleware())
			r.POST("/messages", func(c *gin.Context) {
				stamp++
				restoring = active
				c.Writer.Header().Set("Content-Length", "999")
				c.JSON(200, gin.H{"payload": "mixed"})
			})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/messages", nil))
			if rec.Code != 503 || strings.Contains(rec.Body.String(), "mixed") || rec.Header().Get("Content-Length") != "" || epochCalls != 1 {
				t.Fatalf("fence failed: %d %v %s epoch reads=%d", rec.Code, rec.Header(), rec.Body.String(), epochCalls)
			}
		})
	}
}
func TestContentEpochLocalFenceKeepsOneControllerRead(t *testing.T) {
	calls := 0
	s := &Server{contentEpoch: func(context.Context) (uint64, error) { calls++; return 7, nil }, contentReadFence: func() (uint64, bool) { return 2, false }}
	r := gin.New()
	r.Use(s.messageContentEpochMiddleware())
	r.POST("/messages", func(c *gin.Context) { c.JSON(200, gin.H{"payload": "stable"}) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/messages", nil))
	if rec.Code != 200 || calls != 1 || rec.Header().Get("X-WK-Content-Epoch") != "7" {
		t.Fatalf("status=%d calls=%d headers=%v", rec.Code, calls, rec.Header())
	}
}
