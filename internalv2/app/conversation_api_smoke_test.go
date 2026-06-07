package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestConversationListAPIReadsMembershipAndChannelLatestProjection(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"
	channelID := channelv2.ChannelID{ID: "room-conversation-api", Type: frame.ChannelTypeGroup}
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})
	startCtx, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	node, ok := app.cluster.(*clusterv2.Node)
	if !ok {
		t.Fatalf("cluster runtime = %T, want *clusterv2.Node", app.cluster)
	}
	waitSingleNodeClusterRouteLeader(t, node, channelID.ID, cfg.NodeID)
	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	handler := apiSrv.Handler()
	postAppJSON(t, handler, "/channel", `{"channel_id":"room-conversation-api","channel_type":2,"reset":1,"subscribers":["u-list","u-other"]}`, http.StatusOK)
	sendBody := postAppJSON(t, handler, "/message/send", `{"from_uid":"sender","channel_id":"room-conversation-api","channel_type":2,"client_msg_no":"client-conv-api-1","payload":"aGVsbG8="}`, http.StatusOK)
	var sendResp struct {
		MessageID  int64  `json:"message_id"`
		MessageSeq uint64 `json:"message_seq"`
		Reason     uint8  `json:"reason"`
	}
	if err := json.Unmarshal(sendBody, &sendResp); err != nil {
		t.Fatalf("decode send response: %v", err)
	}
	if sendResp.Reason != uint8(frame.ReasonSuccess) || sendResp.MessageSeq == 0 || sendResp.MessageID == 0 {
		t.Fatalf("send response = %#v, want successful committed message", sendResp)
	}

	flushCtx, flushCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer flushCancel()
	if err := app.conversationProjector.Flush(flushCtx); err != nil {
		t.Fatalf("conversation projector flush error = %v", err)
	}

	listBody := postAppJSON(t, handler, "/conversation/list", `{"uid":"u-list","limit":10}`, http.StatusOK)
	var listResp struct {
		Conversations []struct {
			ChannelID        string `json:"channel_id"`
			ChannelType      int64  `json:"channel_type"`
			LastMessageID    uint64 `json:"last_message_id"`
			LastMessageIDStr string `json:"last_message_idstr"`
			LastMessageSeq   uint64 `json:"last_message_seq"`
			FromUID          string `json:"from_uid"`
			ClientMsgNo      string `json:"client_msg_no"`
			Payload          []byte `json:"payload"`
		} `json:"conversations"`
		More               int  `json:"more"`
		Truncated          bool `json:"truncated"`
		ScannedMemberships int  `json:"scanned_memberships"`
	}
	if err := json.Unmarshal(listBody, &listResp); err != nil {
		t.Fatalf("decode conversation list response: %v body=%s", err, string(listBody))
	}
	if len(listResp.Conversations) != 1 {
		t.Fatalf("conversation count = %d body=%s, want one", len(listResp.Conversations), string(listBody))
	}
	got := listResp.Conversations[0]
	if got.ChannelID != channelID.ID || got.ChannelType != int64(channelID.Type) ||
		got.LastMessageID != uint64(sendResp.MessageID) || got.LastMessageSeq != sendResp.MessageSeq ||
		got.FromUID != "sender" || got.ClientMsgNo != "client-conv-api-1" || string(got.Payload) != "hello" {
		t.Fatalf("conversation = %#v send=%#v, want latest sent message", got, sendResp)
	}
	if listResp.More != 0 || listResp.Truncated || listResp.ScannedMemberships == 0 {
		t.Fatalf("list metadata = more:%d truncated:%v scanned:%d, want complete scanned page", listResp.More, listResp.Truncated, listResp.ScannedMemberships)
	}
}

func TestConversationListAPIPaginatesWithNextCursor(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"
	firstChannel := channelv2.ChannelID{ID: "room-conversation-page-old", Type: frame.ChannelTypeGroup}
	secondChannel := channelv2.ChannelID{ID: "room-conversation-page-new", Type: frame.ChannelTypeGroup}
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})
	startCtx, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	node, ok := app.cluster.(*clusterv2.Node)
	if !ok {
		t.Fatalf("cluster runtime = %T, want *clusterv2.Node", app.cluster)
	}
	waitSingleNodeClusterRouteLeader(t, node, firstChannel.ID, cfg.NodeID)
	waitSingleNodeClusterRouteLeader(t, node, secondChannel.ID, cfg.NodeID)
	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	handler := apiSrv.Handler()
	postAppJSON(t, handler, "/channel", `{"channel_id":"room-conversation-page-old","channel_type":2,"reset":1,"subscribers":["u-page"]}`, http.StatusOK)
	postAppJSON(t, handler, "/channel", `{"channel_id":"room-conversation-page-new","channel_type":2,"reset":1,"subscribers":["u-page"]}`, http.StatusOK)
	postAppJSON(t, handler, "/message/send", `{"from_uid":"sender","channel_id":"room-conversation-page-old","channel_type":2,"client_msg_no":"client-page-old","payload":"b2xk"}`, http.StatusOK)
	time.Sleep(2 * time.Millisecond)
	postAppJSON(t, handler, "/message/send", `{"from_uid":"sender","channel_id":"room-conversation-page-new","channel_type":2,"client_msg_no":"client-page-new","payload":"bmV3"}`, http.StatusOK)

	flushCtx, flushCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer flushCancel()
	if err := app.conversationProjector.Flush(flushCtx); err != nil {
		t.Fatalf("conversation projector flush error = %v", err)
	}

	firstPage := decodeConversationListSmokeResponse(t, postAppJSON(t, handler, "/conversation/list", `{"uid":"u-page","limit":1}`, http.StatusOK))
	if len(firstPage.Conversations) != 1 || firstPage.Conversations[0].ChannelID != secondChannel.ID ||
		firstPage.Conversations[0].ClientMsgNo != "client-page-new" {
		t.Fatalf("first page = %#v, want newest channel", firstPage.Conversations)
	}
	if firstPage.More != 1 || firstPage.NextCursor == nil {
		t.Fatalf("first page metadata = more:%d cursor:%#v, want next cursor", firstPage.More, firstPage.NextCursor)
	}

	nextReq, err := json.Marshal(map[string]any{
		"uid":    "u-page",
		"limit":  1,
		"cursor": firstPage.NextCursor,
	})
	if err != nil {
		t.Fatalf("marshal next request: %v", err)
	}
	secondPage := decodeConversationListSmokeResponse(t, postAppJSON(t, handler, "/conversation/list", string(nextReq), http.StatusOK))
	if len(secondPage.Conversations) != 1 || secondPage.Conversations[0].ChannelID != firstChannel.ID ||
		secondPage.Conversations[0].ClientMsgNo != "client-page-old" {
		t.Fatalf("second page = %#v, want older channel", secondPage.Conversations)
	}
	if secondPage.More != 0 || secondPage.NextCursor != nil || secondPage.Truncated {
		t.Fatalf("second page metadata = more:%d cursor:%#v truncated:%v, want complete final page", secondPage.More, secondPage.NextCursor, secondPage.Truncated)
	}
}

type conversationListSmokeCursor struct {
	LastAt         int64  `json:"last_at"`
	LastMessageSeq uint64 `json:"last_message_seq"`
	ChannelID      string `json:"channel_id"`
	ChannelType    int64  `json:"channel_type"`
}

type conversationListSmokeResponse struct {
	Conversations []struct {
		ChannelID        string `json:"channel_id"`
		ChannelType      int64  `json:"channel_type"`
		LastMessageID    uint64 `json:"last_message_id"`
		LastMessageIDStr string `json:"last_message_idstr"`
		LastMessageSeq   uint64 `json:"last_message_seq"`
		FromUID          string `json:"from_uid"`
		ClientMsgNo      string `json:"client_msg_no"`
		Payload          []byte `json:"payload"`
	} `json:"conversations"`
	NextCursor         *conversationListSmokeCursor `json:"next_cursor"`
	More               int                          `json:"more"`
	Truncated          bool                         `json:"truncated"`
	ScannedMemberships int                          `json:"scanned_memberships"`
}

func decodeConversationListSmokeResponse(t *testing.T, body []byte) conversationListSmokeResponse {
	t.Helper()
	var resp conversationListSmokeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode conversation list response: %v body=%s", err, string(body))
	}
	return resp
}

func postAppJSON(t *testing.T, handler http.Handler, path, body string, want int) []byte {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s status = %d body = %s, want %d", path, rec.Code, rec.Body.String(), want)
	}
	return append([]byte(nil), rec.Body.Bytes()...)
}
