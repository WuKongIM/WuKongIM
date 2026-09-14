//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestMessageUpdateSingleNodeClusterHTTPFlow(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	node := app.cluster.(*cluster.Node)
	waitSingleNodeClusterRouteLeader(t, node, "alice", cfg.NodeID)
	waitSingleNodeClusterRouteLeader(t, node, "bob", cfg.NodeID)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	handler := app.api.(*accessapi.Server).Handler()
	send := postAppJSON(t, handler, "/message/send", `{"from_uid":"alice","channel_id":"bob","channel_type":1,"client_msg_no":"edit-original","payload":"b2xk","header":{"red_dot":1}}`, http.StatusOK)
	var sent struct {
		MessageID  uint64 `json:"message_id"`
		MessageSeq uint64 `json:"message_seq"`
	}
	if err = json.Unmarshal(send, &sent); err != nil || sent.MessageID == 0 {
		t.Fatalf("send=%s err=%v", send, err)
	}
	// Person directory projection is asynchronous to SEND acknowledgement.
	for {
		_, found, e := node.GetUserChannelMembership(ctx, "bob", channelid.EncodePersonChannel("alice", "bob"), 1)
		if e != nil {
			t.Fatal(e)
		}
		if found {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	initBody := postAppJSON(t, handler, "/channel/messageupdates", `{"login_uid":"bob","channel_id":"alice","channel_type":1}`, http.StatusOK)
	var init struct {
		Cursor string `json:"next_update_cursor"`
		Reset  bool   `json:"reset_required"`
	}
	if err = json.Unmarshal(initBody, &init); err != nil || !init.Reset || init.Cursor == "" {
		t.Fatalf("init=%s %v", initBody, err)
	}
	before := postAppJSON(t, handler, "/conversation/list", `{"uid":"bob","limit":10}`, http.StatusOK)
	request := fmt.Sprintf(`{"login_uid":"alice","channel_id":"bob","channel_type":1,"message_id":"%d","expected_content_epoch":"0","expected_version":"0","request_id":"edit-1","payload":"bmV3"}`, sent.MessageID)
	postAppJSON(t, handler, "/message/update", strings.Replace(request, `"expected_content_epoch":"0"`, `"expected_content_epoch":"1"`, 1), http.StatusConflict)
	updated := postAppJSON(t, handler, "/message/update", request, http.StatusOK)
	var update struct {
		Data struct {
			Version    string `json:"version"`
			MessageSeq string `json:"message_seq"`
		} `json:"data"`
	}
	if err = json.Unmarshal(updated, &update); err != nil || update.Data.Version != "1" || update.Data.MessageSeq != fmt.Sprint(sent.MessageSeq) {
		t.Fatalf("update=%s %v", updated, err)
	}
	retry := postAppJSON(t, handler, "/message/update", request, http.StatusOK)
	if string(retry) != string(updated) {
		t.Fatalf("retry changed result: %s %s", retry, updated)
	}
	postAppJSON(t, handler, "/message/update", fmt.Sprintf(`{"login_uid":"alice","channel_id":"bob","channel_type":1,"message_id":"%d","expected_content_epoch":"0","expected_version":"0","request_id":"edit-conflict","payload":"bmV3"}`, sent.MessageID), http.StatusConflict)
	changes := postAppJSON(t, handler, "/channel/messageupdates", fmt.Sprintf(`{"login_uid":"bob","channel_id":"alice","channel_type":1,"update_cursor":%q}`, init.Cursor), http.StatusOK)
	var page struct {
		Updates []struct {
			MessageID string `json:"message_id"`
			Version   string `json:"version"`
			Payload   []byte `json:"payload"`
		}
		Cursor string `json:"next_update_cursor"`
		More   bool   `json:"more"`
	}
	if err = json.Unmarshal(changes, &page); err != nil || len(page.Updates) != 1 || page.Updates[0].Version != "1" || string(page.Updates[0].Payload) != "new" || page.Updates[0].MessageID != fmt.Sprint(sent.MessageID) || page.More {
		t.Fatalf("changes=%s %v", changes, err)
	}
	empty := postAppJSON(t, handler, "/channel/messageupdates", fmt.Sprintf(`{"login_uid":"bob","channel_id":"alice","channel_type":1,"update_cursor":%q}`, page.Cursor), http.StatusOK)
	var emptyPage struct{ Updates []json.RawMessage }
	if err = json.Unmarshal(empty, &emptyPage); err != nil || len(emptyPage.Updates) != 0 {
		t.Fatalf("empty=%s %v", empty, err)
	}
	for path, body := range map[string]string{
		"/messages":            fmt.Sprintf(`{"login_uid":"bob","channel_id":"alice","channel_type":1,"message_ids":[%d]}`, sent.MessageID),
		"/channel/messagesync": `{"login_uid":"bob","channel_id":"alice","channel_type":1,"limit":10}`,
	} {
		response := postAppJSON(t, handler, path, body, http.StatusOK)
		var messages struct {
			Messages []struct {
				Version string `json:"version"`
				Payload []byte `json:"payload"`
			}
		}
		if err = json.Unmarshal(response, &messages); err != nil || len(messages.Messages) != 1 || messages.Messages[0].Version != "1" || string(messages.Messages[0].Payload) != "new" {
			t.Fatalf("%s=%s %v", path, response, err)
		}
	}
	after := postAppJSON(t, handler, "/conversation/list", `{"uid":"bob","limit":10}`, http.StatusOK)
	var oldList, newList struct {
		Conversations []struct {
			ActiveAt    int64  `json:"active_at"`
			Unread      uint64 `json:"unread"`
			LastMessage struct {
				Version string `json:"version"`
				Payload []byte `json:"payload"`
			} `json:"last_message"`
		}
	}
	if err = json.Unmarshal(before, &oldList); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(after, &newList); err != nil {
		t.Fatal(err)
	}
	if len(newList.Conversations) != 1 || len(oldList.Conversations) != 1 || newList.Conversations[0].ActiveAt != oldList.Conversations[0].ActiveAt || newList.Conversations[0].Unread != oldList.Conversations[0].Unread || string(newList.Conversations[0].LastMessage.Payload) != "new" || newList.Conversations[0].LastMessage.Version != "1" {
		t.Fatalf("list before=%s after=%s", before, after)
	}
	legacy := postAppJSON(t, handler, "/conversation/sync", fmt.Sprintf(`{"uid":"bob","msg_count":1,"version":1,"last_msg_seqs":"alice:1:%d"}`, sent.MessageSeq), http.StatusOK)
	var recents []struct {
		Recents []struct {
			Version string `json:"version"`
			Payload []byte `json:"payload"`
		}
	}
	if err = json.Unmarshal(legacy, &recents); err != nil || len(recents) != 1 || len(recents[0].Recents) != 1 || recents[0].Recents[0].Version != "1" || string(recents[0].Recents[0].Payload) != "new" {
		t.Fatalf("legacy=%s %v", legacy, err)
	}
	for path, body := range map[string]string{"/channel/messagesync": `{"login_uid":"bob","channel_id":"alice","channel_type":1,"limit":100}`, "/conversation/list": `{"uid":"bob","limit":100}`} {
		samples := make([]time.Duration, 100)
		for i := range samples {
			start := time.Now()
			postAppJSON(t, handler, path, body, http.StatusOK)
			samples[i] = time.Since(start)
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		t.Logf("%s single-channel HTTP: median=%s p95=%s", path, samples[50], samples[95])
	}

	// Edited payload growth must not make persisted preview scans mistake a short
	// byte-limited batch for the end of history.
	secondBody := postAppJSON(t, handler, "/message/send", `{"from_uid":"alice","channel_id":"bob","channel_type":1,"client_msg_no":"edit-original-2","payload":"b2xk","header":{"red_dot":1}}`, http.StatusOK)
	var second struct {
		MessageID uint64 `json:"message_id"`
	}
	if err = json.Unmarshal(secondBody, &second); err != nil || second.MessageID == 0 {
		t.Fatal("second send", err)
	}
	for i, target := range []uint64{sent.MessageID, second.MessageID} {
		version := "0"
		if i == 0 {
			version = "1"
		}
		body, e := json.Marshal(map[string]any{"login_uid": "alice", "channel_id": "bob", "channel_type": 1, "message_id": fmt.Sprint(target), "expected_content_epoch": "0", "expected_version": version, "request_id": fmt.Sprintf("large-%d", i), "payload": bytes.Repeat([]byte{byte('a' + i)}, 600<<10)})
		if e != nil {
			t.Fatal(e)
		}
		postAppJSON(t, handler, "/message/update", string(body), http.StatusOK)
	}
	largeRecents := postAppJSON(t, handler, "/conversation/sync", `{"uid":"bob","msg_count":2}`, http.StatusOK)
	if err = json.Unmarshal(largeRecents, &recents); err != nil || len(recents) != 1 || len(recents[0].Recents) != 2 || len(recents[0].Recents[0].Payload) != 600<<10 || len(recents[0].Recents[1].Payload) != 600<<10 {
		t.Fatalf("large recents lost byte-limited continuation: rows=%d err=%v", len(recents), err)
	}
	// Editing the old first message cannot replace the second message as the tail.
	finalList := postAppJSON(t, handler, "/conversation/list", `{"uid":"bob","limit":10}`, http.StatusOK)
	if err = json.Unmarshal(finalList, &newList); err != nil || len(newList.Conversations) != 1 || len(newList.Conversations[0].LastMessage.Payload) != 600<<10 || newList.Conversations[0].LastMessage.Payload[0] != 'b' {
		t.Fatal("old message edit replaced newer tail", err)
	}

}
