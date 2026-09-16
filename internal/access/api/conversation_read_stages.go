package api

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"time"
)

// ConversationReadStageObserver optionally extends ConversationListObserver.
// Handler spans bind through synchronous response write, excluding outer
// middleware and client receive/decode. Response spans DTO construction and JSON.
type ConversationReadStageObserver interface {
	// ConversationReadStageObservationEnabled reports whether any consumer needs timing.
	ConversationReadStageObservationEnabled() bool
	ObserveConversationReadStage(scope, stage, result string, duration time.Duration)
}

type conversationReadTimer struct {
	observer ConversationReadStageObserver
	scope    string
}

func (s *Server) conversationReadTimer(scope string) conversationReadTimer {
	if s == nil {
		return conversationReadTimer{}
	}
	observer, _ := s.conversationObserver.(ConversationReadStageObserver)
	if observer != nil && !observer.ConversationReadStageObservationEnabled() {
		observer = nil
	}
	return conversationReadTimer{observer: observer, scope: scope}
}
func (t conversationReadTimer) start() time.Time {
	if t.observer == nil {
		return time.Time{}
	}
	return time.Now()
}
func (t conversationReadTimer) finish(c *gin.Context, stage string, start time.Time, succeeded bool) {
	if t.observer == nil {
		return
	}
	result := "error"
	if succeeded && c.Writer.Status() < http.StatusBadRequest && len(c.Errors) == 0 && c.Request.Context().Err() == nil {
		result = "ok"
	}
	t.observer.ObserveConversationReadStage(t.scope, stage, result, time.Since(start))
}
