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

// Keep each policy closed while its actual Slot leader changes, then check
// post-write freshness at every ingress and the complete committed history.
func TestSendBanSurvivesManualLeaderTransfer(t *testing.T) {
	started := time.Now().UTC()
	var evidence []map[string]any
	defer func() {
		path := os.Getenv("WK_E2E_SEND_BAN_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-send-ban-report.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "started_at": started, "finished_at": time.Now().UTC(), "nodes": 3, "hash_slots": 256, "permission_cache_ttl": "1h", "observations": evidence}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path+".leader-transfer.json", append(raw, '\n'), 0644))
	}()
	opts := []suite.Option{suite.WithManagerHTTP()}
	for i := uint64(1); i <= 3; i++ {
		opts = append(opts, suite.WithNodeConfigOverrides(i, map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_MESSAGE_PERMISSION_CACHE_TTL": "1h"}))
	}
	cluster := suite.New(t).StartThreeNodeCluster(opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx))
	_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	placements := map[uint16]suite.SlotDTO{}
	for _, slot := range cluster.ManagerClient(t, 1).MustSlots(t) {
		if slot.HashSlots != nil {
			for _, h := range slot.HashSlots.Items {
				placements[h] = slot
			}
		}
	}
	const uid = "transfer-ban-user"
	userSlot := placements[uint16(crc32.ChecksumIEEE([]byte(uid))%256)]
	var room string
	var channelSlot suite.SlotDTO
	for i := 0; i < 10000; i++ {
		id := fmt.Sprintf("transfer-ban-room-%d", i)
		slot := placements[uint16(crc32.ChecksumIEEE([]byte(id))%256)]
		if slot.SlotID != userSlot.SlotID {
			room, channelSlot = id, slot
			break
		}
	}
	require.NotEmpty(t, room)
	require.NoError(t, suite.PostChannel(ctx, cluster.MustNode(1).APIAddr(), map[string]any{"channel_id": room, "channel_type": 2, "subscribers": []string{uid}}))
	body := func(no string) map[string]any {
		return map[string]any{"from_uid": uid, "channel_id": room, "channel_type": 2, "client_msg_no": no, "payload": base64.StdEncoding.EncodeToString([]byte(no))}
	}
	successful := []string{}
	send := func(nodeID uint64, label string, want frame.ReasonCode) {
		t.Helper()
		response, err := suite.PostMessageSendEventually(ctx, cluster.MustNode(nodeID).APIAddr(), body(label))
		require.NoError(t, err)
		require.Equal(t, uint8(want), response.Reason)
		if want == frame.ReasonSuccess {
			successful = append(successful, label)
		} else {
			require.Zero(t, response.MessageID)
			require.Zero(t, response.MessageSeq)
		}
		evidence = append(evidence, map[string]any{"operation": "send", "ingress": nodeID, "label": label, "reason": response.Reason, "at": time.Now().UTC()})
	}
	send(1, "transfer-warm", frame.ReasonSuccess)
	for _, scope := range []struct {
		name string
		slot suite.SlotDTO
	}{{"user", userSlot}, {"channel", channelSlot}} {
		set := func(nodeID uint64, value int) {
			t.Helper()
			request := map[string]any{"send_ban": value}
			if scope.name == "user" {
				request["uid"] = uid
			} else {
				request["channel_id"], request["channel_type"] = room, 2
			}
			var out struct {
				Data policy `json:"data"`
			}
			_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(nodeID).APIAddr()+"/"+scope.name+"/send_ban", request, &out)
			require.NoError(t, err)
			require.Equal(t, value, out.Data.Ban)
			evidence = append(evidence, map[string]any{"operation": "policy-write", "scope": scope.name, "ingress": nodeID, "value": value, "version": out.Data.Version, "at": time.Now().UTC()})
		}
		set(1, 1)
		source := scope.slot.Runtime.LeaderID
		for _, slot := range cluster.ManagerClient(t, 1).MustSlots(t) {
			if slot.SlotID == scope.slot.SlotID {
				source = slot.Runtime.LeaderID
			}
		}
		require.NotZero(t, source)
		target := source%3 + 1
		type result struct {
			node         uint64
			no           string
			response     suite.MessageSendResponse
			err          error
			began, ended time.Time
		}
		results := make(chan result, 48)
		gate := make(chan struct{})
		for nodeID := uint64(1); nodeID <= 3; nodeID++ {
			go func(nodeID uint64) {
				<-gate
				for i := 0; i < 16; i++ {
					no := fmt.Sprintf("transfer-%s-%d-%d", scope.name, nodeID, i)
					began := time.Now().UTC()
					callCtx, callCancel := context.WithTimeout(ctx, 10*time.Second)
					response, err := suite.PostMessageSend(callCtx, cluster.MustNode(nodeID).APIAddr(), body(no))
					callCancel()
					results <- result{nodeID, no, response, err, began, time.Now().UTC()}
				}
			}(nodeID)
		}
		close(gate)
		var accepted struct {
			SlotID       uint32 `json:"slot_id"`
			ActualLeader uint64 `json:"actual_leader"`
			Created      bool   `json:"created"`
		}
		transferAt := time.Now().UTC()
		_, err := suite.PostJSON(ctx, fmt.Sprintf("http://%s/manager/slots/%d/leader-transfer", cluster.MustNode(1).ManagerAddr(), scope.slot.SlotID), map[string]any{"target_node": target}, &accepted)
		require.NoError(t, err)
		require.True(t, accepted.Created)
		require.Equal(t, source, accepted.ActualLeader)
		evidence = append(evidence, map[string]any{"operation": "transfer", "scope": scope.name, "slot": scope.slot.SlotID, "source": source, "preferred_target": target, "began": transferAt, "accepted_at": time.Now().UTC()})
		for i := 0; i < 48; i++ {
			out := <-results
			status := http.StatusOK
			if out.err != nil {
				var statusErr *suite.HTTPStatusError
				require.True(t, errors.As(out.err, &statusErr), "%s: %v", out.no, out.err)
				status = statusErr.StatusCode
				require.Equal(t, http.StatusServiceUnavailable, status)
			} else {
				require.Equal(t, uint8(frame.ReasonSendBan), out.response.Reason)
			}
			require.Zero(t, out.response.MessageID)
			require.Zero(t, out.response.MessageSeq)
			evidence = append(evidence, map[string]any{"operation": "transfer-send", "scope": scope.name, "ingress": out.node, "label": out.no, "status": status, "reason": out.response.Reason, "began": out.began, "ended": out.ended})
		}
		var nextLeader uint64
		require.Eventually(t, func() bool {
			var inventory struct {
				Items []suite.SlotDTO `json:"items"`
			}
			_, err := suite.GetJSON(ctx, "http://"+cluster.MustNode(1).ManagerAddr()+"/manager/slots", &inventory)
			if err != nil {
				return false
			}
			for _, slot := range inventory.Items {
				if slot.SlotID == scope.slot.SlotID && slot.Task == nil && slot.Runtime.LeaderID != 0 && slot.Runtime.LeaderID != source {
					nextLeader = slot.Runtime.LeaderID
					return true
				}
			}
			return false
		}, 30*time.Second, 100*time.Millisecond)
		stable, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
		require.NoError(t, err)
		for _, leader := range stable.Leaders {
			if leader.SlotID == scope.slot.SlotID {
				nextLeader = leader.LeaderID
			}
		}
		require.NotEqual(t, source, nextLeader)
		evidence = append(evidence, map[string]any{"operation": "converged", "scope": scope.name, "actual_leader": nextLeader, "at": time.Now().UTC()})
		for n := uint64(1); n <= 3; n++ {
			send(n, fmt.Sprintf("%s-after-transfer-banned-%d", scope.name, n), frame.ReasonSendBan)
		}
		set(source, 0)
		for n := uint64(1); n <= 3; n++ {
			send(n, fmt.Sprintf("%s-after-unban-%d", scope.name, n), frame.ReasonSuccess)
		}
		set(nextLeader, 1)
		for n := uint64(1); n <= 3; n++ {
			send(n, fmt.Sprintf("%s-after-reban-%d", scope.name, n), frame.ReasonSendBan)
		}
		set(source, 0)
	}
	send(3, "transfer-recovered", frame.ReasonSuccess)
	var history struct {
		More     int `json:"more"`
		Messages []struct {
			ClientMsgNo string `json:"client_msg_no"`
		} `json:"messages"`
	}
	require.Eventually(t, func() bool {
		_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/channel/messagesync", map[string]any{"login_uid": uid, "channel_id": room, "channel_type": 2, "limit": 100}, &history)
		return err == nil && len(history.Messages) >= len(successful)
	}, 10*time.Second, 100*time.Millisecond)
	require.Zero(t, history.More)
	var seen []string
	for _, message := range history.Messages {
		seen = append(seen, message.ClientMsgNo)
	}
	require.ElementsMatch(t, successful, seen)
	evidence = append(evidence, map[string]any{"operation": "complete-history", "messages": seen})
}
