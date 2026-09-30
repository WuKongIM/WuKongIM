//go:build e2e

package send_ban

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// Public replies are retained without importing product diagnostics internals.
// Each query selects one of the fixed 64 completed burst requests after the
// window. Empty node results remain explicit rather than becoming zero cost.
func permissionRequestTimeline(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, ingress uint64, acks []permissionBaselineAck) []map[string]any {
	t.Helper()
	require.Len(t, acks, 64)
	out := make([]map[string]any, 0, len(acks))
	for _, ack := range acks {
		request := map[string]any{"client_msg_no": ack.ID, "client": ack}
		nodes := make(map[uint64]json.RawMessage, 3)
		request["nodes"] = nodes
		out = append(out, request)
		for _, node := range cluster.Nodes {
			raw, err := permissionTimelineQuery(ctx, node.APIAddr(), ack.ID)
			require.NoError(t, err)
			nodes[node.Spec.ID] = raw
			var reply struct {
				NodeID uint64 `json:"node_id"`
				Events []struct {
					ClientMsgNo string    `json:"client_msg_no"`
					Stage       string    `json:"stage"`
					At          time.Time `json:"at"`
					Duration    int64     `json:"duration"`
				} `json:"events"`
			}
			require.NoError(t, json.Unmarshal(raw, &reply))
			require.Equal(t, node.Spec.ID, reply.NodeID)
			require.Less(t, len(reply.Events), 32, "full query limit makes completeness unknown")
			stages := map[string]bool{}
			for _, event := range reply.Events {
				require.Equal(t, ack.ID, event.ClientMsgNo, "foreign request in exact lookup")
				require.False(t, event.At.IsZero())
				require.GreaterOrEqual(t, event.Duration, int64(0))
				stages[event.Stage] = true
			}
			if node.Spec.ID == ingress {
				for _, stage := range []string{"gateway.async_dispatch_wait", "gateway.messages_send", "gateway.write_sendack", "message.permission", "message.append_admission_wait"} {
					if !stages[stage] {
						t.Errorf("request timeline %s missing ingress stage %s", ack.ID, stage)
					}
				}
			}
		}
		require.False(t, ack.PendingStartedAt.IsZero())
		require.False(t, ack.WriteStartedAt.Before(ack.PendingStartedAt))
		require.False(t, ack.DecodedAt.Before(ack.WriteStartedAt))
		require.False(t, ack.BridgeAt.Before(ack.DecodedAt))
	}
	return out
}

func permissionTimelineQuery(ctx context.Context, addr, id string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	query := url.Values{"client_msg_no": []string{id}, "limit": []string{"32"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/debug/diagnostics/message?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("timeline status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 64<<10 {
		return nil, fmt.Errorf("timeline exceeds 64 KiB")
	}
	for _, field := range []string{`"from_uid":`, `"payload":`, `"token":`} {
		if strings.Contains(string(raw), field) {
			return nil, fmt.Errorf("timeline exposed forbidden field")
		}
	}
	return raw, nil
}
