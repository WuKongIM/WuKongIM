package api

import (
	"bytes"
	"encoding/json"
	conversation "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	message "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacyBlankClientNumberUsesSameStablePreviewKey(t *testing.T) {
	raw := message.SyncedMessage{MessageID: 99, MessageSeq: 9, ClientMsgNo: "", ChannelID: "g", ChannelType: 2, Expire: 37, Payload: []byte("history")}
	got := newLegacyMessageResp("u", raw)
	if got.ClientMsgNo != "wk3-legacy-99" || raw.ClientMsgNo != "" || got.Expire != 37 || string(got.Payload) != "history" {
		t.Fatalf("read projection: %+v", got)
	}
	conversations := &recordingLegacyConversationSync{result: conversation.LegacySyncResult{Items: []conversation.LegacyConversation{{ChannelID: "g", ChannelType: 2, LastMessageSeq: 9, Recents: []conversation.LegacyRecentMessage{{MessageID: 99, MessageSeq: 9, ChannelID: "g", ChannelType: 2}}}}}}
	srv := New(Options{Conversations: conversations})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{"uid":"u","msg_count":1}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	var rows []conversationSyncLegacyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].LastClientMsgNo != got.ClientMsgNo || rows[0].Recents[0].ClientMsgNo != got.ClientMsgNo {
		t.Fatalf("preview identity: %s %v", rec.Body, err)
	}
}
