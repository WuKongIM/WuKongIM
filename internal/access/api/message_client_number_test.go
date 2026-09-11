package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
)

func TestHTTPMessageSendSuppliesAndReturnsClientNumber(t *testing.T) {
	seen := map[string]bool{}
	for _, input := range []string{"", "  ", "provided-key", "provided-key"} {
		messages := &recordingMessageUsecase{sendResult: messageusecase.SendResult{MessageID: 99, MessageSeq: 7, Reason: messageusecase.ReasonSuccess}}
		srv := New(Options{Messages: messages})
		body, _ := json.Marshal(map[string]any{"channel_id": "g", "channel_type": 2, "client_msg_no": input, "payload": "aGk="})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewReader(body)))
		if rec.Code != http.StatusOK || len(messages.sendCalls) != 1 {
			t.Fatalf("send failed: %d %s", rec.Code, rec.Body)
		}
		key := messages.sendCalls[0].ClientMsgNo
		if input == "provided-key" {
			if key != input {
				t.Fatalf("changed caller idempotency key: %q", key)
			}
		} else {
			if key == "" || key == "  " || seen[key] {
				t.Fatalf("missing or reused generated client number: %q", key)
			}
			seen[key] = true
		}
		var response map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response["client_msg_no"] != key {
			t.Fatalf("response lost persisted client number: %s", rec.Body)
		}
	}
}
