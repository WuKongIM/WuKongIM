//go:build e2e

package send_ban

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// A two-voter UID Slot can lose quorum while its non-replica ingress stays alive.
// Drive real remote permission admission, then prove bounded failure and recovery.
func TestRemotePermissionOverloadFailsClosed(t *testing.T) {
	started := time.Now().UTC()
	var evidence []map[string]any
	defer func() {
		path := os.Getenv("WK_E2E_SEND_BAN_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-send-ban-report.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "started_at": started, "finished_at": time.Now().UTC(), "nodes": 3, "hash_slots": 256, "slot_replicas": 2, "source_revision": os.Getenv("WK_E2E_SOURCE_REVISION"), "observations": evidence}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path+".network-admission.json", append(raw, '\n'), 0644))
	}()
	opts := []suite.Option{suite.WithManagerHTTP()}
	for i := uint64(1); i <= 3; i++ {
		opts = append(opts, suite.WithNodeConfigOverrides(i, map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_CLUSTER_SLOT_REPLICA_N": "2", "WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_MESSAGE_PERMISSION_CACHE_TTL": "1h"}))
	}
	cluster := suite.New(t).StartThreeNodeCluster(opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx))
	_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	const uid, room = "network-admission-user", "network-admission-room"
	hashSlot := uint16(crc32.ChecksumIEEE([]byte(uid)) % 256)
	var ownerID, peerID, ingressID uint64
	for _, slot := range cluster.ManagerClient(t, 1).MustSlots(t) {
		if slot.HashSlots == nil {
			continue
		}
		for _, h := range slot.HashSlots.Items {
			if h != hashSlot {
				continue
			}
			require.Len(t, slot.Runtime.CurrentVoters, 2)
			ownerID = slot.Runtime.LeaderID
			for _, voter := range slot.Runtime.CurrentVoters {
				if voter != ownerID {
					peerID = voter
				}
			}
			for candidate := uint64(1); candidate <= 3; candidate++ {
				if candidate != ownerID && candidate != peerID {
					ingressID = candidate
				}
			}
		}
	}
	require.NotZero(t, ownerID)
	require.NotZero(t, peerID)
	require.NotZero(t, ingressID)
	owner, ingress := cluster.MustNode(ownerID), cluster.MustNode(ingressID)
	evidence = append(evidence, map[string]any{"owner": ownerID, "stopped_voter": peerID, "non_replica_ingress": ingressID, "uid_hash_slot": hashSlot})
	require.NoError(t, suite.PostChannel(ctx, ingress.APIAddr(), map[string]any{"channel_id": room, "channel_type": 2, "subscribers": []string{uid}}))
	body := func(no string) map[string]any {
		return map[string]any{"from_uid": uid, "channel_id": room, "channel_type": 2, "client_msg_no": no, "payload": base64.StdEncoding.EncodeToString([]byte(no))}
	}
	warm, err := suite.PostMessageSendEventually(ctx, ingress.APIAddr(), body("network-warm"))
	require.NoError(t, err)
	require.Equal(t, uint8(frame.ReasonSuccess), warm.Reason)
	beforeOwner, err := suite.FetchMetricSamples(ctx, owner.APIAddr())
	require.NoError(t, err)
	beforeIngress, err := suite.FetchMetricSamples(ctx, ingress.APIAddr())
	require.NoError(t, err)
	require.NoError(t, cluster.MustNode(peerID).Stop())
	type result struct {
		index             int
		response          suite.MessageSendResponse
		err               error
		started, finished time.Time
	}
	const requests = 192
	gate := make(chan struct{})
	results := make(chan result, requests)
	for i := 0; i < requests; i++ {
		go func(index int) {
			<-gate
			at := time.Now().UTC()
			callCtx, callCancel := context.WithTimeout(ctx, 15*time.Second)
			defer callCancel()
			response, err := suite.PostMessageSend(callCtx, ingress.APIAddr(), body(fmt.Sprintf("network-denied-%d", index)))
			results <- result{index: index, response: response, err: err, started: at, finished: time.Now().UTC()}
		}(i)
	}
	// Separate arrivals beyond the fixed collection window. A same-fact
	// simultaneous burst now coalesces and cannot prove actual saturation.
	arrivals := time.NewTicker(5 * time.Millisecond)
	for i := 0; i < requests; i++ {
		<-arrivals.C
		gate <- struct{}{}
	}
	arrivals.Stop()
	close(gate)
	evidence = append(evidence, map[string]any{"requests": requests, "arrival_separation_ms": 5, "kind": "quorum-loss backpressure fault; not a throughput gate"})
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	peak := float64(0)
	completed := 0
	for completed < requests {
		select {
		case out := <-results:
			completed++
			var statusErr *suite.HTTPStatusError
			require.True(t, errors.As(out.err, &statusErr), "request %d: %v", out.index, out.err)
			require.Equal(t, http.StatusServiceUnavailable, statusErr.StatusCode)
			require.Zero(t, out.response.MessageID)
			require.Zero(t, out.response.MessageSeq)
			evidence = append(evidence, map[string]any{"index": out.index, "status": statusErr.StatusCode, "started_at": out.started, "finished_at": out.finished})
		case <-ticker.C:
			samples, err := suite.FetchMetricSamples(ctx, owner.APIAddr())
			require.NoError(t, err)
			active := suite.SumMetricSamples(samples, "wukongim_message_permission_inflight", nil)
			peak = max(peak, active)
			require.LessOrEqual(t, active, float64(64))
			ingressSamples, sampleErr := suite.FetchMetricSamples(ctx, ingress.APIAddr())
			require.NoError(t, sampleErr)
			for kind, limit := range map[string]float64{"calls": 1024, "cohorts": 64, "budget_bytes": 16 << 20} {
				owned := suite.SumMetricSamples(ingressSamples, "wukongim_message_permission_cohort_owned", map[string]string{"kind": kind})
				require.LessOrEqual(t, owned, limit)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	afterOwner, err := suite.FetchMetricSamples(ctx, owner.APIAddr())
	require.NoError(t, err)
	afterIngress, err := suite.FetchMetricSamples(ctx, ingress.APIAddr())
	require.NoError(t, err)
	busyLabels := map[string]string{"stage": "admission", "result": "busy"}
	busy := suite.SumMetricSamples(afterOwner, "wukongim_message_permission_duration_seconds_count", busyLabels) - suite.SumMetricSamples(beforeOwner, "wukongim_message_permission_duration_seconds_count", busyLabels)
	envelopes := suite.SumMetricSamples(afterIngress, "wukongim_message_permission_counts_total", map[string]string{"kind": "node_envelopes"}) - suite.SumMetricSamples(beforeIngress, "wukongim_message_permission_counts_total", map[string]string{"kind": "node_envelopes"})
	cohortBusy := suite.SumMetricSamples(afterIngress, "wukongim_message_permission_counts_total", map[string]string{"kind": "cohort_busy"}) - suite.SumMetricSamples(beforeIngress, "wukongim_message_permission_counts_total", map[string]string{"kind": "cohort_busy"})
	require.Positive(t, busy+cohortBusy, "the fault must actually saturate permission admission")
	require.Positive(t, envelopes)
	require.Positive(t, peak)
	require.Eventually(t, func() bool {
		samples, err := suite.FetchMetricSamples(ctx, owner.APIAddr())
		if err != nil || suite.SumMetricSamples(samples, "wukongim_message_permission_inflight", nil) != 0 {
			return false
		}
		samples, err = suite.FetchMetricSamples(ctx, ingress.APIAddr())
		if err != nil {
			return false
		}
		for _, kind := range []string{"calls", "cohorts", "budget_bytes"} {
			if suite.SumMetricSamples(samples, "wukongim_message_permission_cohort_owned", map[string]string{"kind": kind}) != 0 {
				return false
			}
		}
		return true
	}, 10*time.Second, 100*time.Millisecond)
	evidence = append(evidence, map[string]any{"remote_node_envelopes": envelopes, "owner_admission_busy": busy, "ingress_cohort_busy": cohortBusy, "sampled_peak_inflight": peak, "terminal_inflight": 0})
	require.NoError(t, cluster.StartStoppedNode(peerID))
	require.NoError(t, cluster.WaitHTTPReady(ctx))
	_, err = cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	recovered, err := suite.PostMessageSendEventually(ctx, ingress.APIAddr(), body("network-recovered"))
	require.NoError(t, err)
	require.Equal(t, uint8(frame.ReasonSuccess), recovered.Reason)
	var history struct {
		More     int `json:"more"`
		Messages []struct {
			ClientMsgNo string `json:"client_msg_no"`
		} `json:"messages"`
	}
	require.Eventually(t, func() bool {
		_, err := suite.PostJSON(ctx, "http://"+ingress.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": uid, "channel_id": room, "channel_type": 2, "limit": 100}, &history)
		return err == nil && len(history.Messages) >= 2
	}, 10*time.Second, 100*time.Millisecond)
	require.Zero(t, history.More)
	var seen []string
	for _, message := range history.Messages {
		seen = append(seen, message.ClientMsgNo)
	}
	require.ElementsMatch(t, []string{"network-warm", "network-recovered"}, seen)
	evidence = append(evidence, map[string]any{"exact_history": seen})
}
