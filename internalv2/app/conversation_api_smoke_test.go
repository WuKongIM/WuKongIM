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

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestConversationListAPIReadsAuthorityCacheAfterRecipientDispatch(t *testing.T) {
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
	node, ok := app.cluster.(*clusterv2.Node)
	if !ok {
		t.Fatalf("cluster runtime = %T, want *clusterv2.Node", app.cluster)
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
	if page.More != 0 {
		t.Fatalf("list metadata more = %d, want complete page", page.More)
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
	node, ok := app.cluster.(*clusterv2.Node)
	if !ok {
		t.Fatalf("cluster runtime = %T, want *clusterv2.Node", app.cluster)
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
		More int `json:"more"`
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
	if got.ChannelID != "receiver" || got.ChannelType != int64(frame.ChannelTypePerson) || got.ActiveAt <= 0 ||
		got.Unread != 0 || got.LastMessage.MessageID != uint64(sendResp.MessageID) ||
		got.LastMessage.MessageSeq != sendResp.MessageSeq || got.LastMessage.FromUID != "sender" ||
		got.LastMessage.ClientMsgNo != "client-conv-api-1" || string(got.LastMessage.Payload) != "hello" {
		t.Fatalf("conversation = %#v send=%#v, want latest sent message read by sender", got, sendResp)
	}
	if listResp.More != 0 {
		t.Fatalf("list metadata more = %d, want complete page", listResp.More)
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
	firstChannel := channelv2.ChannelID{ID: "room-conversation-page-old", Type: frame.ChannelTypeGroup}
	secondChannel := channelv2.ChannelID{ID: "room-conversation-page-new", Type: frame.ChannelTypeGroup}
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
	node, ok := app.cluster.(*clusterv2.Node)
	if !ok {
		t.Fatalf("cluster runtime = %T, want *clusterv2.Node", app.cluster)
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
	time.Sleep(2 * time.Millisecond)
	postAppJSON(t, handler, "/message/send", `{"from_uid":"sender","channel_id":"room-conversation-page-new","channel_type":2,"client_msg_no":"client-page-new","payload":"bmV3"}`, http.StatusOK)

	upsertAppConversationStates(t, node, []metadb.ConversationState{
		{
			UID:          "u-page",
			Kind:         metadb.ConversationKindNormal,
			ChannelID:    firstChannel.ID,
			ChannelType:  int64(firstChannel.Type),
			ActiveAt:     firstActiveAt,
			UpdatedAt:    firstActiveAt + 1,
			SparseActive: true,
		},
		{
			UID:         "u-page",
			Kind:        metadb.ConversationKindNormal,
			ChannelID:   secondChannel.ID,
			ChannelType: int64(secondChannel.Type),
			ActiveAt:    secondActiveAt,
			UpdatedAt:   secondActiveAt + 1,
		},
	})

	firstPage := decodeConversationListSmokeResponse(t, postAppJSON(t, handler, "/conversation/list", `{"uid":"u-page","limit":1}`, http.StatusOK))
	if len(firstPage.Conversations) != 1 || firstPage.Conversations[0].ChannelID != secondChannel.ID ||
		firstPage.Conversations[0].LastMessage == nil || firstPage.Conversations[0].LastMessage.ClientMsgNo != "client-page-new" {
		t.Fatalf("first page = %#v, want newest channel", firstPage.Conversations)
	}
	if firstPage.More != 1 || firstPage.NextCursor == nil {
		t.Fatalf("first page metadata = more:%d cursor:%#v, want next cursor", firstPage.More, firstPage.NextCursor)
	}
	if firstPage.NextCursor.ActiveAt != secondActiveAt || firstPage.NextCursor.ChannelID != secondChannel.ID {
		t.Fatalf("first page cursor = %#v, want newest active row cursor", firstPage.NextCursor)
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
		secondPage.Conversations[0].LastMessage == nil || secondPage.Conversations[0].LastMessage.ClientMsgNo != "client-page-old" ||
		!secondPage.Conversations[0].SparseActive {
		t.Fatalf("second page = %#v, want older channel", secondPage.Conversations)
	}
	if secondPage.More != 0 || secondPage.NextCursor != nil {
		t.Fatalf("second page metadata = more:%d cursor:%#v, want complete final page", secondPage.More, secondPage.NextCursor)
	}
}

type conversationListSmokeCursor struct {
	ActiveAt    int64  `json:"active_at"`
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
}

type conversationListSmokeResponse struct {
	Conversations []struct {
		ChannelID    string `json:"channel_id"`
		ChannelType  int64  `json:"channel_type"`
		ActiveAt     int64  `json:"active_at"`
		Unread       uint64 `json:"unread"`
		SparseActive bool   `json:"sparse_active"`
		LastMessage  *struct {
			MessageID    uint64 `json:"message_id"`
			MessageIDStr string `json:"message_idstr"`
			MessageSeq   uint64 `json:"message_seq"`
			FromUID      string `json:"from_uid"`
			ClientMsgNo  string `json:"client_msg_no"`
			Payload      []byte `json:"payload"`
		} `json:"last_message"`
	} `json:"conversations"`
	NextCursor *conversationListSmokeCursor `json:"next_cursor"`
	More       int                          `json:"more"`
}

func decodeConversationListSmokeResponse(t *testing.T, body []byte) conversationListSmokeResponse {
	t.Helper()
	var resp conversationListSmokeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode conversation list response: %v body=%s", err, string(body))
	}
	return resp
}

func upsertAppConversationStates(t *testing.T, node *clusterv2.Node, states []metadb.ConversationState) {
	t.Helper()
	for _, state := range states {
		waitSingleNodeClusterRouteLeader(t, node, state.UID, node.NodeID())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := node.UpsertConversationStatesBatch(ctx, states); err != nil {
		t.Fatalf("UpsertConversationStatesBatch() error = %v", err)
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
