//go:build integration

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/require"
)

// Failure inventory: a permission Ready fact or completed cold preparation may
// outlive directory deletion/recreation; router retry may silently use the new
// generation; rejected preparation must not change the committed tail. The real
// before-send webhook pins the interval without introducing a product test hook.
func TestPersonDirectoryGenerationRejectsRecreatedSourceBeforeAppend(t *testing.T) {
	for _, tc := range []struct {
		name      string
		batchSize int
	}{{"scalar_hot", 0}, {"batch_one_cold", 1}, {"batch_many_cold", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			batch := tc.batchSize > 0
			arrived, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					ClientMsgNo string `json:"client_msg_no"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				if body.ClientMsgNo == "generation-race" {
					close(arrived)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"allow":true}`))
			}))
			defer callback.Close()
			defer once.Do(func() { close(release) })
			cfg := singleNodeClusterAppConfig(t)
			cfg.Cluster.Slots.HashSlotCount = 256
			cfg.Webhook.BeforeSend = BeforeSendWebhookConfig{Enabled: true, HTTPAddr: callback.URL, Timeout: 10 * time.Second}
			a, err := New(cfg)
			require.NoError(t, err)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				require.NoError(t, a.Stop(ctx))
			})
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			require.NoError(t, a.Start(ctx))
			node := a.cluster.(*cluster.Node)
			waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
			id := channelid.EncodePersonChannel("generation-a", "generation-b")
			waitSingleNodeClusterRouteLeader(t, node, id, cfg.NodeID)
			cmd := message.SendCommand{FromUID: "generation-a", ChannelID: id, ChannelType: 1, ClientMsgNo: "warm", Payload: []byte("body")}
			if !batch {
				r, e := a.Messages().Send(ctx, cmd)
				require.NoError(t, e)
				require.Equal(t, message.ReasonSuccess, r.Reason)
			}
			cmd.ClientMsgNo = "generation-race"
			type outcome struct {
				result       message.SendResult
				err          error
				batchResults []message.SendBatchItemResult
			}
			done := make(chan outcome, 1)
			go func() {
				if batch {
					items := make([]message.SendBatchItem, tc.batchSize)
					for i := range items {
						items[i] = message.SendBatchItem{Context: ctx, Command: cmd}
						if i > 0 {
							items[i].Command.ClientMsgNo = "queued-extra"
						}
					}
					out := a.Messages().SendBatch(items)
					done <- outcome{result: out[0].Result, err: out[0].Err, batchResults: out}
				} else {
					r, e := a.Messages().Send(ctx, cmd)
					done <- outcome{result: r, err: e}
				}
			}()
			select {
			case <-arrived:
			case <-ctx.Done():
				t.Fatal("before-send callback was not reached")
			}
			// Keep the replacement Pending so a successful append cannot be explained by
			// a fresh completed preparation racing this test.
			require.NoError(t, a.personDirectoryProjector.Stop(ctx))
			old, err := node.GetChannelMetadataAuthoritative(ctx, id, 1)
			require.NoError(t, err)
			require.Equal(t, metadb.DirectoryProjectionReady, old.DirectoryProjectionState)
			before, err := node.CommittedChannelTail(ctx, id, 1)
			require.NoError(t, err)
			require.NoError(t, node.DeleteChannelMetadata(ctx, id, 1))
			require.Equal(t, []error{nil}, node.AdmitPersonDirectoryTasks(ctx, []metadb.PersonDirectoryTask{{ChannelID: id, ChannelType: 1, CommittedTail: before}}))
			current, err := node.GetChannelMetadataAuthoritative(ctx, id, 1)
			require.NoError(t, err)
			require.Greater(t, current.DirectoryProjectionGeneration, old.DirectoryProjectionGeneration)
			require.Equal(t, metadb.DirectoryProjectionPending, current.DirectoryProjectionState)
			once.Do(func() { close(release) })
			var got outcome
			select {
			case got = <-done:
			case <-ctx.Done():
				t.Fatal("SEND did not finish")
			}
			after, err := node.CommittedChannelTail(ctx, id, 1)
			require.NoError(t, err)
			evidence, _ := json.Marshal(map[string]any{"batch": batch, "old_generation": old.DirectoryProjectionGeneration, "new_generation": current.DirectoryProjectionGeneration, "before_tail": before, "after_tail": after, "reason": got.result.Reason, "error": errorText1066(got.err)})
			t.Logf("generation_evidence=%s", evidence)
			require.Error(t, got.err, "old Ready must not admit this invocation into a replacement directory generation")
			require.NotEqual(t, message.ReasonSuccess, got.result.Reason)
			for _, result := range got.batchResults {
				require.Error(t, result.Err)
			}
			require.Equal(t, before, after, "rejected invocation must not append")
			require.NoError(t, a.personDirectoryProjector.Start(ctx))
			cmd.ClientMsgNo = "fresh-generation"
			r, e := a.Messages().Send(ctx, cmd)
			require.NoError(t, e)
			require.Equal(t, message.ReasonSuccess, r.Reason)
		})
	}
}
func errorText1066(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// An unavailable projector must fail before append. Recovery must retain the
// pending task and allow the first actual committed message to use sequence 1.
func TestPersonDirectoryUnavailablePreventsAppend(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	a, err := New(cfg)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	t.Cleanup(func() {
		stop, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		require.NoError(t, a.Stop(stop))
	})
	require.NoError(t, a.Start(ctx))
	node := a.cluster.(*cluster.Node)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	id := channelid.EncodePersonChannel("unavailable-a", "unavailable-b")
	waitSingleNodeClusterRouteLeader(t, node, id, cfg.NodeID)
	require.NoError(t, a.personDirectoryProjector.Stop(ctx))
	cmd := message.SendCommand{FromUID: "unavailable-a", ChannelID: id, ChannelType: 1, ClientMsgNo: "blocked", Payload: []byte("body")}
	attempt, done := context.WithTimeout(ctx, time.Second)
	failed, err := a.Messages().Send(attempt, cmd)
	done()
	require.Error(t, err)
	require.Zero(t, failed.MessageSeq)
	require.NoError(t, a.personDirectoryProjector.Start(ctx))
	cmd.ClientMsgNo = "recovered"
	recovered, err := a.Messages().Send(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, message.ReasonSuccess, recovered.Reason)
	require.EqualValues(t, 1, recovered.MessageSeq)
	t.Logf("preparation_failure_evidence: blocked_sequence=%d recovered_sequence=%d", failed.MessageSeq, recovered.MessageSeq)
}
