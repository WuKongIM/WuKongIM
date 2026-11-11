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
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

// This control creates ordinary v3 groups through the public API, without any
// imported data. An authorized empty read must not create message runtime state.
func TestEmptyGroupHistoryHTTPBeforeFirstSend(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d_node_cluster", count), func(t *testing.T) {
			voters := make([]cluster.ControlVoter, count)
			for i := range voters {
				voters[i] = cluster.ControlVoter{NodeID: uint64(i + 1), Addr: freeSendackSmokeTCPAddr(t)}
			}
			configs := make([]Config, count)
			for i, v := range voters {
				cfg := singleNodeClusterAppConfig(t)
				cfg.NodeID = v.NodeID
				cfg.Cluster.NodeID = v.NodeID
				cfg.Cluster.ListenAddr = v.Addr
				cfg.Cluster.Control.Voters = voters
				cfg.Cluster.Slots.HashSlotCount = 256
				cfg.Cluster.Slots.ReplicaCount = uint16(count)
				cfg.Cluster.Channel.ReplicaCount = uint16(count)
				cfg.API.ListenAddr = "127.0.0.1:0"
				configs[i] = cfg
			}
			var apps []*App
			stop := func() {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				done := make(chan error, len(apps))
				for _, a := range apps {
					go func() { done <- a.Stop(ctx) }()
				}
				for range apps {
					require.NoError(t, <-done)
				}
				apps = nil
			}
			t.Cleanup(stop)
			start := func() {
				t.Helper()
				var nodes []*cluster.Node
				for _, cfg := range configs {
					a, err := New(cfg, WithLogger(wklog.NewNop()))
					require.NoError(t, err)
					apps = append(apps, a)
					nodes = append(nodes, a.cluster.(*cluster.Node))
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				done := make(chan error, len(apps))
				for _, a := range apps {
					go func() { done <- a.Start(ctx) }()
				}
				for range apps {
					require.NoError(t, <-done)
				}
				waitAppClusterSnapshotsConverge(t, nodes)
			}
			start()
			postAppJSON(t, apps[0].api.(*accessapi.Server).Handler(), "/channel", `{"channel_id":"empty-history-group","channel_type":2,"subscribers":["empty-reader","empty-sender"]}`, http.StatusOK)
			for run := 0; run < 2; run++ {
				for _, a := range apps {
					// Prove this is the same absent-runtime condition as a fully
					// excluded history, rather than an already-initialized empty log.
					_, err := a.cluster.(*cluster.Node).GetChannelRuntimeMeta(context.Background(), "empty-history-group", 2)
					require.ErrorIs(t, err, meta.ErrNotFound)
					h := a.api.(*accessapi.Server).Handler()
					raw := postAppJSON(t, h, "/channel/messagesync", `{"login_uid":"empty-reader","channel_id":"empty-history-group","channel_type":2,"limit":10}`, http.StatusOK)
					var page struct {
						Messages []json.RawMessage `json:"messages"`
						More     int               `json:"more"`
					}
					require.NoError(t, json.Unmarshal(raw, &page))
					require.NotNil(t, page.Messages)
					require.Empty(t, page.Messages)
					require.Zero(t, page.More)
					raw = postAppJSON(t, h, "/channel/messagesyncbatch", `{"login_uid":"empty-reader","items":[{"channel_id":"empty-history-group","channel_type":2,"limit":10}]}`, http.StatusOK)
					var batch struct {
						Items []struct {
							Messages []json.RawMessage `json:"messages"`
							Error    string            `json:"error"`
						} `json:"items"`
					}
					require.NoError(t, json.Unmarshal(raw, &batch))
					require.Len(t, batch.Items, 1)
					require.Empty(t, batch.Items[0].Error)
					require.NotNil(t, batch.Items[0].Messages)
					require.Empty(t, batch.Items[0].Messages)
					raw = postAppJSON(t, h, "/conversation/sync", `{"uid":"empty-reader","msg_count":10}`, http.StatusOK)
					require.JSONEq(t, `[]`, string(raw))
					postAppJSON(t, h, "/channel/messagesync", `{"login_uid":"outsider","channel_id":"empty-history-group","channel_type":2,"limit":10}`, http.StatusBadRequest)
					_, err = a.cluster.(*cluster.Node).GetChannelRuntimeMeta(context.Background(), "empty-history-group", 2)
					require.ErrorIs(t, err, meta.ErrNotFound, "empty reads must not create message runtime state")
				}
				if run == 0 {
					stop()
					start()
				}
			}
			// Restart readiness does not pre-activate this deliberately absent
			// Channel. Retry only the explicit recovery response, with the same
			// body and idempotency key so the first committed sequence stays one.
			const firstSend = `{"from_uid":"empty-sender","channel_id":"empty-history-group","channel_type":2,"client_msg_no":"first-normal-send","payload":"aGVsbG8="}`
			var rec *httptest.ResponseRecorder
			require.Eventually(t, func() bool {
				rec = httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/message/send", strings.NewReader(firstSend))
				req.Header.Set("Content-Type", "application/json")
				apps[count-1].api.(*accessapi.Server).Handler().ServeHTTP(rec, req)
				var failure struct {
					Error string `json:"error"`
				}
				return rec.Code != http.StatusServiceUnavailable || json.Unmarshal(rec.Body.Bytes(), &failure) != nil || failure.Error != "retry required"
			}, 5*time.Second, 10*time.Millisecond)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			raw := rec.Body.Bytes()
			var ack struct {
				MessageSeq uint64 `json:"message_seq"`
			}
			require.NoError(t, json.Unmarshal(raw, &ack))
			require.EqualValues(t, 1, ack.MessageSeq)
			for _, a := range apps {
				raw := postAppJSON(t, a.api.(*accessapi.Server).Handler(), "/channel/messagesync", `{"login_uid":"empty-reader","channel_id":"empty-history-group","channel_type":2,"limit":10}`, http.StatusOK)
				var page struct {
					Messages []struct {
						MessageSeq  uint64 `json:"message_seq"`
						ClientMsgNo string `json:"client_msg_no"`
					} `json:"messages"`
				}
				require.NoError(t, json.Unmarshal(raw, &page))
				require.Len(t, page.Messages, 1)
				require.EqualValues(t, 1, page.Messages[0].MessageSeq)
				require.Equal(t, "first-normal-send", page.Messages[0].ClientMsgNo)
			}
		})
	}
}
