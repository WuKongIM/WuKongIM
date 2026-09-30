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
func permissionRequestTimeline(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, ingress uint64, acks []permissionBaselineAck, out *[]map[string]any) {
	t.Helper()
	require.Len(t, acks, 64)
	*out = make([]map[string]any, 0, len(acks))
	for _, ack := range acks {
		request := map[string]any{"client_msg_no": ack.ID, "client": ack}
		nodes := make(map[uint64]any, 3)
		for _, node := range cluster.Nodes {
			nodes[node.Spec.ID] = map[string]string{"status": "not_queried"}
		}
		request["nodes"] = nodes
		*out = append(*out, request)
	}
	for i, ack := range acks {
		nodes := (*out)[i]["nodes"].(map[uint64]any)
		for _, node := range cluster.Nodes {
			fail := func(code string) {
				nodes[node.Spec.ID] = map[string]string{"status": "query_failed", "error_code": code}
				t.Errorf("request timeline %s node %d: %s", ack.ID, node.Spec.ID, code)
			}
			raw, err := permissionTimelineQuery(ctx, node.APIAddr(), ack.ID)
			if err != nil {
				fail("unavailable_or_unsafe_reply")
				continue
			}
			var reply struct {
				NodeID uint64 `json:"node_id"`
				Events []struct {
					ClientMsgNo string    `json:"client_msg_no"`
					Stage       string    `json:"stage"`
					At          time.Time `json:"at"`
					Duration    int64     `json:"duration"`
				} `json:"events"`
			}
			if err := json.Unmarshal(raw, &reply); err != nil || reply.NodeID != node.Spec.ID {
				fail("invalid_reply_identity")
				continue
			}
			if len(reply.Events) >= 32 {
				fail("query_limit_completeness_unknown")
				continue
			}
			stages := map[string]bool{}
			valid := true
			for _, event := range reply.Events {
				if event.ClientMsgNo != ack.ID || event.At.IsZero() || event.Duration < 0 {
					valid = false
					break
				}
				stages[event.Stage] = true
			}
			if !valid {
				fail("foreign_or_invalid_event")
				continue
			}
			nodes[node.Spec.ID] = raw
			if node.Spec.ID == ingress {
				for _, stage := range []string{"gateway.async_dispatch_wait", "gateway.messages_send", "gateway.write_sendack", "message.permission", "message.append_admission_wait"} {
					if !stages[stage] {
						t.Errorf("request timeline %s missing ingress stage %s", ack.ID, stage)
					}
				}
			}
		}
		if ack.PendingStartedAt.IsZero() || ack.WriteStartedAt.Before(ack.PendingStartedAt) || ack.DecodedAt.Before(ack.WriteStartedAt) || ack.BridgeAt.Before(ack.DecodedAt) {
			t.Errorf("request timeline %s invalid client timing", ack.ID)
		}
	}
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
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("timeline invalid JSON")
	}
	if permissionTimelineUnsafe(value) {
		return nil, fmt.Errorf("timeline exposed forbidden field")
	}
	return raw, nil
}

// Check decoded keys so whitespace or JSON escaping cannot hide private data.
func permissionTimelineUnsafe(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			switch strings.ToLower(key) {
			case "from_uid", "payload", "token":
				return true
			}
			if permissionTimelineUnsafe(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if permissionTimelineUnsafe(child) {
				return true
			}
		}
	}
	return false
}
