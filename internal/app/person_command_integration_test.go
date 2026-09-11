//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	channelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// TestPersonCommandHTTPCluster exercises the original system command through real
// cluster authority, including cross-node append and CMD directory/read RPCs.
func TestPersonCommandHTTPCluster(t *testing.T) {
	for _, test := range []struct {
		name, suffix string
		nodes        int
	}{
		{"single-node cluster default suffix", "", 1},
		{"three-node cluster custom suffix", "__commands", 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			voters := make([]cluster.ControlVoter, test.nodes)
			for i := range voters {
				voters[i] = cluster.ControlVoter{NodeID: uint64(i + 1), Addr: freeSendackSmokeTCPAddr(t)}
			}
			apps := make([]*App, 0, test.nodes)
			// These nodes share one process supervisor, so stop them together.
			defer t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				stopped := make(chan error, len(apps))
				for _, a := range apps {
					go func() { stopped <- a.Stop(ctx) }()
				}
				for range apps {
					if err := <-stopped; err != nil {
						t.Error(err)
					}
				}
			})
			nodes := make([]*cluster.Node, 0, test.nodes)
			for _, voter := range voters {
				cfg := singleNodeClusterAppConfig(t)
				cfg.NodeID = voter.NodeID
				cfg.Cluster.NodeID = voter.NodeID
				cfg.Cluster.ListenAddr = voter.Addr
				cfg.Cluster.Control.Voters = voters
				cfg.Cluster.Slots.HashSlotCount = 256
				cfg.Cluster.Slots.ReplicaCount = uint16(test.nodes)
				cfg.Cluster.Channel.ReplicaCount = uint16(test.nodes)
				cfg.Message.CMDChannelSuffix = test.suffix
				cfg.API.ListenAddr = "127.0.0.1:0"
				cfg.Delivery.Enabled = true
				a, err := newTestApp(t, cfg, WithLogger(wklog.NewNop()))
				if err != nil {
					t.Fatal(err)
				}
				apps = append(apps, a)
				nodes = append(nodes, a.cluster.(*cluster.Node))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			started := make(chan error, len(apps))
			for _, a := range apps {
				go func() { started <- a.Start(ctx) }()
			}
			for range apps {
				if err := <-started; err != nil {
					t.Fatal(err)
				}
			}
			waitAppClusterSnapshotsConverge(t, nodes)
			post := func(a *App, path, body string) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(ctx)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				a.api.(*accessapi.Server).Handler().ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: HTTP %d %s", path, rec.Code, rec.Body.String())
				}
				return rec
			}
			original := `{"header":{"no_persist":1,"red_dot":1,"sync_once":1},"from_uid":"","channel_id":"uu1","channel_type":1,"payload":"eyJ0eXBlIjo5OSwiY21kIjoiY2xlYXJVbnJlYWQiLCJwYXJhbSI6eyJjaGFubmVsSUQiOiJnZmgiLCJjaGFubmVsVHlwZSI6MX19","subscribers":[]}`
			source := channelid.EncodePersonChannel("____system", "uu1")
			codec := channelid.CommandCodec{Suffix: test.suffix}
			// Send through every entry node, guaranteeing remote authority forwarding in a three-node cluster.
			for _, a := range apps {
				rec := post(a, "/message/send", original)
				// Pre-encoded command IDs must use the same codec in the permission layer.
				post(a, "/message/send", strings.Replace(original, `"channel_id":"uu1"`, fmt.Sprintf(`"channel_id":%q`, codec.ToCommandChannel(source)), 1))
				var result struct {
					MessageID  int64  `json:"message_id"`
					MessageSeq uint64 `json:"message_seq"`
					Reason     uint8  `json:"reason"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.MessageID == 0 || result.MessageSeq != 0 || result.Reason != 1 {
					t.Fatalf("transient send=%s err=%v", rec.Body.String(), err)
				}
			}
			command := codec.ToCommandChannel(source)
			tail, err := nodes[0].CommittedChannelTail(ctx, command, 1)
			if err != nil || tail != 0 {
				t.Fatalf("transient command tail=%d err=%v", tail, err)
			}
			if _, found, err := nodes[0].GetUserChannelMembership(ctx, "uu1", source, 1); err != nil || found {
				t.Fatalf("transient command created conversation membership: found=%v err=%v", found, err)
			}
			binding := fmt.Sprintf(`{"uid":"uu1","channel_id":%q,"channel_type":1}`, source)
			post(apps[0], "/message/cmd/bind", binding)
			// More than one read chunk mixes unused logs with the live command below.
			for i := 0; i <= cmdsync.MaxCommandReadBatch; i++ {
				post(apps[i%len(apps)], "/message/cmd/bind", fmt.Sprintf(`{"uid":"uu1","channel_id":"unused-%03d","channel_type":2}`, i))
			}
			// Discovery is installed before the first persistent CMD exists.
			if empty := post(apps[len(apps)-1], "/message/sync", `{"uid":"uu1","limit":10}`); empty.Body.String() != "[]" {
				t.Fatalf("unused binding failed sync: %s", empty.Body)
			}
			// Terminal group closure must not block another source's offline CMD.
			post(apps[0], "/channel", `{"channel_id":"unused-000","channel_type":2,"subscribers":["uu1"]}`)
			post(apps[len(apps)-1], "/channel/delete", `{"channel_id":"unused-000","channel_type":2}`)
			durable := strings.Replace(original, `"no_persist":1`, `"no_persist":0`, 1)
			post(apps[len(apps)-1], "/message/send", durable)
			rec := post(apps[0], "/message/sync", `{"uid":"uu1","limit":10}`)
			var messages []struct {
				ChannelID  string `json:"channel_id"`
				MessageSeq uint64 `json:"message_seq"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &messages); err != nil || len(messages) != 1 || messages[0].ChannelID != "____system" || messages[0].MessageSeq != 1 {
				t.Fatalf("CMD sync=%s err=%v", rec.Body.String(), err)
			}
			post(apps[0], "/message/syncack", `{"uid":"uu1","last_message_seq":1}`)
			empty := post(apps[0], "/message/sync", `{"uid":"uu1","limit":10}`)
			if empty.Body.String() != "[]" {
				t.Fatalf("acked sync=%s", empty.Body.String())
			}
			if err := apps[0].cmdSync.Unbind(ctx, cmdsync.UnbindCommand{UID: "uu1", ChannelID: source, ChannelType: 1}); err != nil {
				t.Fatal(err)
			}

			scopedBinding := `{"subscribers":["offline-b","offline-a","offline-b"]}`
			post(apps[0], "/message/cmd/bind", scopedBinding)
			scopedSend := `{"header":{"sync_once":1},"subscribers":["offline-b","offline-a","offline-b"],"payload":"e30="}`
			post(apps[len(apps)-1], "/message/send", scopedSend)
			// A retry after SEND must not move an existing recipient beyond that command.
			post(apps[len(apps)-1], "/message/cmd/bind", scopedBinding)
			for i, uid := range []string{"offline-a", "offline-b"} {
				entry := apps[i%len(apps)]
				body := fmt.Sprintf(`{"uid":%q,"limit":10}`, uid)
				got := post(entry, "/message/sync", body)
				var records []map[string]interface{}
				if err := json.Unmarshal(got.Body.Bytes(), &records); err != nil || len(records) != 1 {
					t.Fatalf("scoped offline sync: %s err=%v", got.Body, err)
				}
				post(entry, "/message/syncack", fmt.Sprintf(`{"uid":%q,"last_message_seq":1}`, uid))
				for _, reader := range apps {
					if rest := post(reader, "/message/sync", body); rest.Body.String() != "[]" {
						t.Fatalf("ack not durable across nodes: %s", rest.Body)
					}
				}
			}
			post(apps[len(apps)-1], "/message/cmd/unbind", scopedBinding)
			post(apps[0], "/message/send", scopedSend)
			if got := post(apps[0], "/message/sync", `{"uid":"offline-a","limit":10}`); got.Body.String() != "[]" {
				t.Fatalf("unbound scope recovered command: %s", got.Body)
			}
		})
	}
}
