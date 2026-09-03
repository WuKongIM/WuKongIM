//go:build e2e

package suite

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// ChannelRuntimeMeta is the public Manager projection used by black-box E2E scenarios.
type ChannelRuntimeMeta struct {
	ChannelID         string   `json:"channel_id"`
	ChannelType       int64    `json:"channel_type"`
	SlotID            uint32   `json:"slot_id"`
	ChannelEpoch      uint64   `json:"channel_epoch"`
	LeaderEpoch       uint64   `json:"leader_epoch"`
	Leader            uint64   `json:"leader"`
	SlotLeader        uint64   `json:"slot_leader"`
	PreferredLeader   uint64   `json:"preferred_leader"`
	Replicas          []uint64 `json:"replicas"`
	ISR               []uint64 `json:"isr"`
	MinISR            int64    `json:"min_isr"`
	MaxMessageSeq     *uint64  `json:"max_message_seq"`
	Status            string   `json:"status"`
	WriteFenceToken   string   `json:"write_fence_token"`
	WriteFenceVersion uint64   `json:"write_fence_version"`
	WriteFenceReason  string   `json:"write_fence_reason"`
	ActiveTaskID      string   `json:"active_task_id"`
	Degraded          bool     `json:"degraded"`
	DegradedReason    string   `json:"degraded_reason"`
}

type channelRuntimeMetaPage struct {
	Items []ChannelRuntimeMeta `json:"items"`
}

// GetChannelRuntimeMeta reads one exact Channel runtime row through public Manager HTTP.
func GetChannelRuntimeMeta(ctx context.Context, node *StartedNode, channelID string, channelType uint8) (ChannelRuntimeMeta, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	query := url.Values{
		"exact":        []string{"1"},
		"channel_id":   []string{channelID},
		"channel_type": []string{strconv.FormatUint(uint64(channelType), 10)},
	}
	var page channelRuntimeMetaPage
	_, err := GetJSON(requestCtx, "http://"+node.ManagerAddr()+"/manager/channel-runtime-meta?"+query.Encode(), &page)
	if err != nil {
		return ChannelRuntimeMeta{}, err
	}
	for _, item := range page.Items {
		if item.ChannelID == channelID && item.ChannelType == int64(channelType) {
			return item, nil
		}
	}
	return ChannelRuntimeMeta{}, fmt.Errorf("runtime meta for %s/%d not found in %+v", channelID, channelType, page.Items)
}

// RequireChannelRuntimeMetaEventually waits for one active row with an observed Channel Leader.
func RequireChannelRuntimeMetaEventually(t testing.TB, cluster *StartedCluster, node *StartedNode, channelID string, channelType uint8, timeout time.Duration) ChannelRuntimeMeta {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(managerPollInterval)
	defer ticker.Stop()

	var last ChannelRuntimeMeta
	var lastErr error
	for {
		meta, err := GetChannelRuntimeMeta(ctx, node, channelID, channelType)
		if err == nil {
			last = meta
			if meta.Leader != 0 && meta.Status == "active" {
				return meta
			}
			lastErr = fmt.Errorf("runtime meta = %+v, want active Leader", meta)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("channel runtime meta for %s/%d did not converge: last=%+v lastErr=%v\n%s", channelID, channelType, last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}
