package api

import (
	"bytes"
	"context"
	"encoding/json"
	message "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"net/http"
	"net/http/httptest"
	"testing"
)

type lookupMessageUsecaseForTest struct {
	*recordingMessageUsecase
	query message.LookupMessagesQuery
}

func (a *lookupMessageUsecaseForTest) LookupMessages(_ context.Context, q message.LookupMessagesQuery) (message.SyncChannelMessagesResult, error) {
	a.query = q
	return message.SyncChannelMessagesResult{Messages: []message.SyncedMessage{{MessageID: 2098321203545923584, MessageSeq: 3, ClientMsgNo: "key", ChannelID: "g", ChannelType: 2, Expire: 91, Payload: []byte(`{"type":1}`)}}}, nil
}
func TestLegacyMessagesLookupRoute(t *testing.T) {
	app := &lookupMessageUsecaseForTest{recordingMessageUsecase: &recordingMessageUsecase{}}
	s := New(Options{Messages: app})
	r := httptest.NewRecorder()
	q := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBufferString(`{"login_uid":"a","channel_id":"g","channel_type":2,"client_msg_nos":["key"],"message_ids":[2098321203545923584],"message_seqs":[3]}`))
	q.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(r, q)
	if r.Code != 200 {
		t.Fatalf("status %d %s", r.Code, r.Body.String())
	}
	if app.query.LoginUID != "a" || len(app.query.ClientMsgNos) != 1 || app.query.MessageIDs[0] != 2098321203545923584 {
		t.Fatalf("query %+v", app.query)
	}
	var out struct {
		Messages []struct {
			ID      string `json:"message_idstr"`
			Expire  uint32 `json:"expire"`
			Payload []byte `json:"payload"`
		}
	}
	if e := json.Unmarshal(r.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if len(out.Messages) != 1 || out.Messages[0].ID != "2098321203545923584" || out.Messages[0].Expire != 91 || string(out.Messages[0].Payload) != `{"type":1}` {
		t.Fatalf("response %s", r.Body.String())
	}
}
