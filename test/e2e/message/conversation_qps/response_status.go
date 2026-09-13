package conversation_qps

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// responseStatusError distinguishes rejected HTTP requests from malformed successful pages.
type responseStatusError struct {
	code int
	body string
}

func (e *responseStatusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.code, e.body) }

// Legacy sync wraps serving-node and shared request-admission backpressure.
func (e *responseStatusError) capacityRefusal() bool {
	if e.code == http.StatusServiceUnavailable {
		return true
	}
	if e.code != http.StatusBadRequest {
		return false
	}
	var legacy struct {
		Message string `json:"msg"`
		Status  int    `json:"status"`
	}
	if json.Unmarshal([]byte(e.body), &legacy) != nil || legacy.Status != 400 {
		return false
	}
	switch legacy.Message {
	case "internal/usecase/conversation: list capacity exceeded", "internal/message: backpressured: channel: backpressured", "internal/usecase/conversation: route not ready\nchannel: backpressured":
		return true
	default:
		return false
	}
}
