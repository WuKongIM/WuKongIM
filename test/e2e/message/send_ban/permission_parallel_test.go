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

// Prove overlap through real remote owners, not elapsed-time assumptions. Two
// disjoint two-voter Slots leave both owners and a non-replica ingress alive
// after the other voters stop. This is a controlled quorum fault, not HA proof.
func TestRemotePermissionReadsOverlap(t *testing.T) {
	started := time.Now().UTC()
	var evidence []map[string]any
	defer func() {
		path := os.Getenv("WK_E2E_SEND_BAN_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-send-ban-report.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "started_at": started, "finished_at": time.Now().UTC(), "nodes": 5, "hash_slots": 256, "slot_replicas": 2, "source_revision": os.Getenv("WK_E2E_SOURCE_REVISION"), "observations": evidence}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path+".parallel.json", append(raw, '\n'), 0644))
	}()
	opts := []suite.Option{suite.WithManagerHTTP()}
	for i := uint64(1); i <= 5; i++ {
		opts = append(opts, suite.WithNodeConfigOverrides(i, map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_CLUSTER_SLOT_REPLICA_N": "2", "WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_MESSAGE_PERMISSION_CACHE_TTL": "1h"}))
	}
	cluster := suite.New(t).StartStaticCluster(5, opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx))
	_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	slots := cluster.ManagerClient(t, 1).MustSlots(t)
	var userSlot, channelSlot suite.SlotDTO
	var ownerA, ownerB, peerA, peerB, ingressID uint64
	for _, a := range slots {
		for _, b := range slots {
			if a.HashSlots == nil || b.HashSlots == nil || a.Runtime.LeaderID == 0 || b.Runtime.LeaderID == 0 || len(a.Runtime.CurrentVoters) != 2 || len(b.Runtime.CurrentVoters) != 2 {
				continue
			}
			voters := map[uint64]bool{}
			for _, id := range append(append([]uint64(nil), a.Runtime.CurrentVoters...), b.Runtime.CurrentVoters...) {
				voters[id] = true
			}
			if len(voters) != 4 || !voters[a.Runtime.LeaderID] || !voters[b.Runtime.LeaderID] {
				continue
			}
			userSlot, channelSlot = a, b
			ownerA, ownerB = a.Runtime.LeaderID, b.Runtime.LeaderID
			for _, id := range a.Runtime.CurrentVoters {
				if id != ownerA {
					peerA = id
				}
			}
			for _, id := range b.Runtime.CurrentVoters {
				if id != ownerB {
					peerB = id
				}
			}
			for id := uint64(1); id <= 5; id++ {
				if !voters[id] {
					ingressID = id
				}
			}
			break
		}
		if ingressID != 0 {
			break
		}
	}
	require.NotZero(t, ingressID, "no disjoint actual two-voter placements")
	require.NotEqual(t, ownerA, ownerB)
	keyFor := func(prefix string, slot suite.SlotDTO) string {
		t.Helper()
		for i := 0; i < 10000; i++ {
			key := fmt.Sprintf("%s-%d", prefix, i)
			hash := uint16(crc32.ChecksumIEEE([]byte(key)) % 256)
			for _, h := range slot.HashSlots.Items {
				if h == hash {
					return key
				}
			}
		}
		t.Fatal("no bounded fixture identity for selected Slot")
		return ""
	}
	uid, room := keyFor("parallel-user", userSlot), keyFor("parallel-room", channelSlot)
	ingress := cluster.MustNode(ingressID)
	evidence = append(evidence, map[string]any{"operation": "placement", "user_slot": userSlot.SlotID, "channel_slot": channelSlot.SlotID, "owner_a": ownerA, "owner_b": ownerB, "stopped_voters": []uint64{peerA, peerB}, "non_replica_ingress": ingressID, "uid": uid, "channel": room})
	require.NoError(t, suite.PostChannel(ctx, ingress.APIAddr(), map[string]any{"channel_id": room, "channel_type": 2, "subscribers": []string{uid}}))
	body := func(no string) map[string]any {
		return map[string]any{"from_uid": uid, "channel_id": room, "channel_type": 2, "client_msg_no": no, "payload": base64.StdEncoding.EncodeToString([]byte(no))}
	}
	warm, err := suite.PostMessageSendEventually(ctx, ingress.APIAddr(), body("parallel-warm"))
	require.NoError(t, err)
	require.Equal(t, uint8(frame.ReasonSuccess), warm.Reason)
	snapshot := func(id uint64) (float64, float64, float64) {
		t.Helper()
		samples, err := suite.FetchMetricSamples(ctx, cluster.MustNode(id).APIAddr())
		require.NoError(t, err)
		active := suite.SumMetricSamples(samples, "wukongim_message_permission_inflight", nil)
		admitted := suite.SumMetricSamples(samples, "wukongim_message_permission_duration_seconds_count", map[string]string{"stage": "admission", "result": "ok"})
		envelopes := suite.SumMetricSamples(samples, "wukongim_message_permission_counts_total", map[string]string{"kind": "node_envelopes"})
		return active, admitted, envelopes
	}
	_, _, beforeControl := snapshot(ingressID)
	control, err := suite.PostMessageSend(ctx, ingress.APIAddr(), body("parallel-control"))
	require.NoError(t, err)
	require.Equal(t, uint8(frame.ReasonSuccess), control.Reason)
	_, _, afterControl := snapshot(ingressID)
	require.Equal(t, float64(2), afterControl-beforeControl)
	aActive, aBefore, _ := snapshot(ownerA)
	bActive, bBefore, _ := snapshot(ownerB)
	require.Zero(t, aActive)
	require.Zero(t, bActive)
	evidence = append(evidence, map[string]any{"operation": "successful-control", "remote_envelopes": afterControl - beforeControl, "owner_a_admissions_before": aBefore, "owner_b_admissions_before": bBefore})
	require.NoError(t, cluster.MustNode(peerA).Stop())
	require.NoError(t, cluster.MustNode(peerB).Stop())
	type result struct {
		response     suite.MessageSendResponse
		err          error
		began, ended time.Time
	}
	results := make(chan result, 1)
	go func() {
		began := time.Now().UTC()
		callCtx, done := context.WithTimeout(ctx, 15*time.Second)
		defer done()
		response, err := suite.PostMessageSend(callCtx, ingress.APIAddr(), body("parallel-denied"))
		results <- result{response, err, began, time.Now().UTC()}
	}()
	proved := false
	for attempt := 0; attempt < 64 && !proved; attempt++ {
		select {
		case out := <-results:
			evidence = append(evidence, map[string]any{"operation": "early-completion", "began": out.began, "ended": out.ended, "error": fmt.Sprint(out.err)})
			t.Fatal("request completed before an unambiguous overlap observation")
		default:
		}
		atA1 := time.Now().UTC()
		a1, _, _ := snapshot(ownerA)
		if a1 == 1 {
			atB := time.Now().UTC()
			b, _, _ := snapshot(ownerB)
			atA2 := time.Now().UTC()
			a2, _, _ := snapshot(ownerA)
			// This separate later scrape establishes that no second admission
			// replaced A between its two positive observations. Transport RPC
			// totals may be buffered, so use the direct admission counter.
			_, aAfter, _ := snapshot(ownerA)
			_, bAfter, _ := snapshot(ownerB)
			_, _, envelopes := snapshot(ingressID)
			evidence = append(evidence, map[string]any{"operation": "overlap-probe", "a_first_at": atA1, "b_at": atB, "a_second_at": atA2, "a_first_active": a1, "b_active": b, "a_second_active": a2, "a_admission_delta": aAfter - aBefore, "b_admission_delta": bAfter - bBefore, "remote_envelopes": envelopes - afterControl})
			proved = b == 1 && a2 == 1 && aAfter-aBefore == 1 && bAfter-bBefore == 1 && envelopes-afterControl == 2
		}
		if !proved {
			time.Sleep(20 * time.Millisecond)
		}
	}
	require.True(t, proved, "no continuous owner A activity spanning owner B activity")
	out := <-results
	var statusErr *suite.HTTPStatusError
	require.True(t, errors.As(out.err, &statusErr), "%v", out.err)
	require.Equal(t, http.StatusServiceUnavailable, statusErr.StatusCode)
	require.Zero(t, out.response.MessageID)
	require.Zero(t, out.response.MessageSeq)
	evidence = append(evidence, map[string]any{"operation": "failed-closed", "status": statusErr.StatusCode, "began": out.began, "ended": out.ended, "elapsed_ms": out.ended.Sub(out.began).Seconds() * 1000})
	require.Eventually(t, func() bool {
		a, _, _ := snapshot(ownerA)
		b, _, _ := snapshot(ownerB)
		return a == 0 && b == 0
	}, 10*time.Second, 100*time.Millisecond)
	require.NoError(t, cluster.StartStoppedNode(peerA))
	require.NoError(t, cluster.StartStoppedNode(peerB))
	require.NoError(t, cluster.WaitHTTPReady(ctx))
	_, err = cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	recovered, err := suite.PostMessageSendEventually(ctx, ingress.APIAddr(), body("parallel-recovered"))
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
		return err == nil && len(history.Messages) >= 3
	}, 10*time.Second, 100*time.Millisecond)
	require.Zero(t, history.More)
	var seen []string
	for _, message := range history.Messages {
		seen = append(seen, message.ClientMsgNo)
	}
	require.ElementsMatch(t, []string{"parallel-warm", "parallel-control", "parallel-recovered"}, seen)
	evidence = append(evidence, map[string]any{"operation": "recovery", "terminal_inflight": 0, "exact_history": seen})
}
