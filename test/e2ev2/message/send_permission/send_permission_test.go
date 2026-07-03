//go:build e2e

package send_permission

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestWukongIMHTTPMessageSendEnforcesLegacyPermissions(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	assertSendReason := func(t *testing.T, fromUID, channelID string, want frame.ReasonCode) {
		t.Helper()

		resp, err := suite.PostMessageSend(ctx, node.APIAddr(), map[string]any{
			"from_uid":      fromUID,
			"channel_id":    channelID,
			"channel_type":  frame.ChannelTypeGroup,
			"client_msg_no": channelID + "-" + fromUID,
			"payload":       base64.StdEncoding.EncodeToString([]byte("permission check")),
		})
		require.NoError(t, err, node.DumpDiagnostics())
		require.Equal(t, uint8(want), resp.Reason, node.DumpDiagnostics())
		require.Zero(t, resp.MessageID, node.DumpDiagnostics())
		require.Zero(t, resp.MessageSeq, node.DumpDiagnostics())
	}

	t.Run("missing group channel", func(t *testing.T) {
		assertSendReason(t, "perm-missing-sender", "perm-missing-group", frame.ReasonChannelNotExist)
	})

	t.Run("group sender not subscriber", func(t *testing.T) {
		const channelID = "perm-nonmember-group"
		require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
			"channel_id":   channelID,
			"channel_type": frame.ChannelTypeGroup,
			"reset":        1,
			"subscribers":  []string{"perm-nonmember-member"},
		}), node.DumpDiagnostics())

		assertSendReason(t, "perm-nonmember-sender", channelID, frame.ReasonSubscriberNotExist)
	})

	t.Run("group sender denylisted", func(t *testing.T) {
		const channelID = "perm-denylist-group"
		require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
			"channel_id":   channelID,
			"channel_type": frame.ChannelTypeGroup,
			"reset":        1,
			"subscribers":  []string{"perm-denylist-sender"},
		}), node.DumpDiagnostics())
		_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/channel/blacklist_set", map[string]any{
			"channel_id":   channelID,
			"channel_type": frame.ChannelTypeGroup,
			"uids":         []string{"perm-denylist-sender"},
		}, nil)
		require.NoError(t, err, node.DumpDiagnostics())

		assertSendReason(t, "perm-denylist-sender", channelID, frame.ReasonInBlacklist)
	})

	t.Run("group sender missing from nonempty allowlist", func(t *testing.T) {
		const channelID = "perm-allowlist-group"
		require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
			"channel_id":   channelID,
			"channel_type": frame.ChannelTypeGroup,
			"reset":        1,
			"subscribers":  []string{"perm-allowlist-sender"},
		}), node.DumpDiagnostics())
		_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/channel/whitelist_set", map[string]any{
			"channel_id":   channelID,
			"channel_type": frame.ChannelTypeGroup,
			"uids":         []string{"perm-allowlist-other"},
		}, nil)
		require.NoError(t, err, node.DumpDiagnostics())

		assertSendReason(t, "perm-allowlist-sender", channelID, frame.ReasonNotInWhitelist)
	})

	t.Run("sender send ban wins before group lookup", func(t *testing.T) {
		require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
			"channel_id":   "perm-sendban-sender",
			"channel_type": frame.ChannelTypePerson,
			"send_ban":     1,
		}), node.DumpDiagnostics())

		assertSendReason(t, "perm-sendban-sender", "perm-sendban-missing-group", frame.ReasonSendBan)
	})
}
