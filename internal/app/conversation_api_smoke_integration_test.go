//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestConversationListAPIBuildsReceiverConversationFromMembership(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"
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
	node, ok := app.cluster.(*cluster.Node)
	if !ok {
		t.Fatalf("cluster runtime = %T, want *cluster.Node", app.cluster)
	}
	waitSingleNodeClusterRouteLeader(t, node, "sender-cache", cfg.NodeID)
	waitSingleNodeClusterRouteLeader(t, node, "receiver-cache", cfg.NodeID)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	handler := apiSrv.Handler()
	const clientMsgNo = "client-conv-cache-1"
	sendBody := postAppJSON(t, handler, "/message/send", fmt.Sprintf(`{"from_uid":"sender-cache","channel_id":"receiver-cache","channel_type":1,"client_msg_no":%q,"payload":"aGVsbG8="}`, clientMsgNo), http.StatusOK)
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
	var page conversationListSmokeResponse
	waitUntil(t, 3*time.Second, func() bool {
		page = decodeConversationListSmokeResponse(t, postAppJSON(t, handler, "/conversation/list", `{"uid":"receiver-cache","limit":10}`, http.StatusOK))
		return len(page.Conversations) == 1
	})
	if len(page.Conversations) != 1 {
		t.Fatalf("conversation count = %d page=%#v, want one active row after recipient dispatch", len(page.Conversations), page)
	}
	got := page.Conversations[0]
	if got.ChannelID != "sender-cache" || got.ChannelType != int64(frame.ChannelTypePerson) ||
		got.LastMessage == nil || got.LastMessage.ClientMsgNo != clientMsgNo ||
		got.LastMessage.MessageID != uint64(sendResp.MessageID) ||
		got.LastMessage.MessageSeq != sendResp.MessageSeq {
		t.Fatalf("conversation = %#v send=%#v, want authority-cache row with latest sent message", got, sendResp)
	}
	if !page.Done {
		t.Fatalf("list metadata done = false, want complete page")
	}
}

func TestConversationListAPIReadsActiveRowAndLastVisibleMessage(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"
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
	node, ok := app.cluster.(*cluster.Node)
	if !ok {
		t.Fatalf("cluster runtime = %T, want *cluster.Node", app.cluster)
	}
	waitSingleNodeClusterRouteLeader(t, node, "sender", cfg.NodeID)
	waitSingleNodeClusterRouteLeader(t, node, "receiver", cfg.NodeID)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	handler := apiSrv.Handler()
	sendBody := postAppJSON(t, handler, "/message/send", `{"from_uid":"sender","channel_id":"receiver","channel_type":1,"client_msg_no":"client-conv-api-1","payload":"aGVsbG8="}`, http.StatusOK)
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
	var listResp struct {
		Conversations []struct {
			ChannelID   string `json:"channel_id"`
			ChannelType int64  `json:"channel_type"`
			ActiveAt    int64  `json:"active_at"`
			Unread      uint64 `json:"unread"`
			LastMessage *struct {
				MessageID    uint64 `json:"message_id"`
				MessageIDStr string `json:"message_idstr"`
				MessageSeq   uint64 `json:"message_seq"`
				FromUID      string `json:"from_uid"`
				ClientMsgNo  string `json:"client_msg_no"`
				Payload      []byte `json:"payload"`
			} `json:"last_message"`
		} `json:"conversations"`
		Done bool `json:"done"`
	}
	var listBody []byte
	waitUntil(t, 3*time.Second, func() bool {
		listBody = postAppJSON(t, handler, "/conversation/list", `{"uid":"sender","limit":10}`, http.StatusOK)
		if err := json.Unmarshal(listBody, &listResp); err != nil {
			return false
		}
		return len(listResp.Conversations) == 1
	})
	if err := json.Unmarshal(listBody, &listResp); err != nil {
		t.Fatalf("decode conversation list response: %v body=%s", err, string(listBody))
	}
	if len(listResp.Conversations) != 1 {
		t.Fatalf("conversation count = %d body=%s, want one", len(listResp.Conversations), string(listBody))
	}
	got := listResp.Conversations[0]
	if got.LastMessage == nil {
		t.Fatalf("conversation = %#v, want last_message", got)
	}
	if got.ChannelID != "receiver" || got.ChannelType != int64(frame.ChannelTypePerson) || got.ActiveAt != 0 ||
		got.Unread != 0 || got.LastMessage.MessageID != uint64(sendResp.MessageID) ||
		got.LastMessage.MessageSeq != sendResp.MessageSeq || got.LastMessage.FromUID != "sender" ||
		got.LastMessage.ClientMsgNo != "client-conv-api-1" || string(got.LastMessage.Payload) != "hello" {
		t.Fatalf("conversation = %#v send=%#v, want latest sent message read by sender", got, sendResp)
	}
	if !listResp.Done {
		t.Fatal("list metadata done = false, want complete page")
	}

	receiverPage := decodeConversationListSmokeResponse(t, postAppJSON(t, handler, "/conversation/list", `{"uid":"receiver","limit":10}`, http.StatusOK))
	if len(receiverPage.Conversations) != 1 || receiverPage.Conversations[0].ChannelID != "sender" ||
		receiverPage.Conversations[0].ChannelType != int64(frame.ChannelTypePerson) ||
		receiverPage.Conversations[0].Unread != sendResp.MessageSeq ||
		receiverPage.Conversations[0].LastMessage == nil ||
		receiverPage.Conversations[0].LastMessage.ClientMsgNo != "client-conv-api-1" {
		t.Fatalf("receiver conversations = %#v, want sender person conversation", receiverPage.Conversations)
	}
}

func TestConversationListAPIPaginatesWithNextCursor(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"
	firstChannel := channelruntime.ChannelID{ID: "room-conversation-page-old", Type: frame.ChannelTypeGroup}
	secondChannel := channelruntime.ChannelID{ID: "room-conversation-page-new", Type: frame.ChannelTypeGroup}
	firstActiveAt := int64(1000)
	secondActiveAt := int64(2000)
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
	node, ok := app.cluster.(*cluster.Node)
	if !ok {
		t.Fatalf("cluster runtime = %T, want *cluster.Node", app.cluster)
	}
	waitSingleNodeClusterRouteLeader(t, node, firstChannel.ID, cfg.NodeID)
	waitSingleNodeClusterRouteLeader(t, node, secondChannel.ID, cfg.NodeID)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	seedGroupSendPermission(t, node, firstChannel, "sender")
	seedGroupSendPermission(t, node, secondChannel, "sender")
	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	handler := apiSrv.Handler()
	postAppJSON(t, handler, "/message/send", `{"from_uid":"sender","channel_id":"room-conversation-page-old","channel_type":2,"client_msg_no":"client-page-old","payload":"b2xk"}`, http.StatusOK)
	postAppJSON(t, handler, "/message/send", `{"from_uid":"sender","channel_id":"room-conversation-page-new","channel_type":2,"client_msg_no":"client-page-new","payload":"bmV3"}`, http.StatusOK)

	upsertAppMemberships(t, node, []metadb.UserChannelMembership{
		{
			UID: "u-page", ChannelID: firstChannel.ID, ChannelType: int64(firstChannel.Type),
			JoinSeq: 1, ActivatedAt: firstActiveAt, UpdatedAt: firstActiveAt + 1, SourceVersion: 1,
		},
		{
			UID: "u-page", ChannelID: secondChannel.ID, ChannelType: int64(secondChannel.Type),
			JoinSeq: 1, ActivatedAt: secondActiveAt, UpdatedAt: secondActiveAt + 1, SourceVersion: 1,
		},
	})

	firstPage := decodeConversationListSmokeResponse(t, postAppJSON(t, handler, "/conversation/list", `{"uid":"u-page","limit":1}`, http.StatusOK))
	if len(firstPage.Conversations) != 1 || firstPage.Conversations[0].ChannelID != secondChannel.ID ||
		firstPage.Conversations[0].LastMessage == nil || firstPage.Conversations[0].LastMessage.ClientMsgNo != "client-page-new" {
		t.Fatalf("first page = %#v, want newest channel", firstPage.Conversations)
	}
	if firstPage.Done || firstPage.NextCursor == "" {
		t.Fatalf("first page metadata = done:%v cursor:%q, want next cursor", firstPage.Done, firstPage.NextCursor)
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
		secondPage.Conversations[0].LastMessage == nil || secondPage.Conversations[0].LastMessage.ClientMsgNo != "client-page-old" {
		t.Fatalf("second page = %#v, want older channel", secondPage.Conversations)
	}
	if !secondPage.Done {
		t.Fatalf("second page metadata = done:%v cursor:%q, want complete final page", secondPage.Done, secondPage.NextCursor)
	}
}

type conversationListSmokeResponse struct {
	Conversations []struct {
		ChannelID   string `json:"channel_id"`
		ChannelType int64  `json:"channel_type"`
		ActiveAt    int64  `json:"active_at"`
		Unread      uint64 `json:"unread"`
		LastMessage *struct {
			MessageID    uint64 `json:"message_id"`
			MessageIDStr string `json:"message_idstr"`
			MessageSeq   uint64 `json:"message_seq"`
			FromUID      string `json:"from_uid"`
			ClientMsgNo  string `json:"client_msg_no"`
			Payload      []byte `json:"payload"`
		} `json:"last_message"`
	} `json:"conversations"`
	NextCursor string `json:"next_cursor"`
	Done       bool   `json:"done"`
}

func decodeConversationListSmokeResponse(t *testing.T, body []byte) conversationListSmokeResponse {
	t.Helper()
	var resp conversationListSmokeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode conversation list response: %v body=%s", err, string(body))
	}
	return resp
}

func upsertAppMemberships(t *testing.T, node *cluster.Node, states []metadb.UserChannelMembership) {
	t.Helper()
	for _, state := range states {
		waitSingleNodeClusterRouteLeader(t, node, state.UID, node.NodeID())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, state := range states {
		committedTail := uint64(0)
		if state.JoinSeq > 0 {
			committedTail = state.JoinSeq - 1
		}
		if err := node.UpsertUserChannelMemberships(ctx, state.ChannelID, state.ChannelType, []string{state.UID}, committedTail, state.SourceVersion, state.UpdatedAt); err != nil {
			t.Fatalf("UpsertUserChannelMemberships() error = %v", err)
		}
		if state.ActivatedAt > 0 {
			if err := node.ActivateUserChannelMembership(ctx, state.UID, state.ChannelID, state.ChannelType, state.ActivatedAt, state.UpdatedAt); err != nil {
				t.Fatalf("ActivateUserChannelMembership() error = %v", err)
			}
		}
	}
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
