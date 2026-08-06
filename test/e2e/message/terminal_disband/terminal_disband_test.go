//go:build e2e

package terminal_disband

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestTerminalDisbandFencesOrdinarySystemAndSystemDeviceSends(t *testing.T) {
	const (
		channelID    = "terminal-disband-room"
		memberUID    = "terminal-member"
		systemUID    = "terminal-system"
		systemDevice = "terminal-system-device"
		nonmemberUID = "terminal-device-sender"
		initialMsgNo = "terminal-before-delete"
		initialCMDNo = "terminal-cmd-before-delete"
	)
	node := suite.New(t).StartSingleNodeCluster(
		suite.WithNodeConfigOverrides(1, map[string]string{
			"WK_MESSAGE_SYSTEM_DEVICE_ID": systemDevice,
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
		"reset": 1, "subscribers": []string{memberUID},
	}), node.DumpDiagnostics())
	_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/user/systemuids_add", map[string]any{
		"uids": []string{systemUID},
	}, nil)
	require.NoError(t, err, node.DumpDiagnostics())

	initial, err := suite.PostMessageSend(ctx, node.APIAddr(), sendBody(memberUID, channelID, initialMsgNo, "", false))
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), initial.Reason)
	require.NotZero(t, initial.MessageSeq)
	_, err = suite.PostJSON(ctx, "http://"+node.APIAddr()+"/message/cmd/bind", map[string]any{
		"uid": memberUID, "channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
	}, nil)
	require.NoError(t, err, node.DumpDiagnostics())
	initialCMD, err := suite.PostMessageSend(ctx, node.APIAddr(), sendBody(memberUID, channelID, initialCMDNo, "", true))
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), initialCMD.Reason)
	require.NotZero(t, initialCMD.MessageSeq)
	var beforeDeleteCMD []struct {
		ClientMsgNo string `json:"client_msg_no"`
	}
	_, err = suite.PostJSON(ctx, "http://"+node.APIAddr()+"/message/sync", map[string]any{
		"uid": memberUID, "message_seq": 0, "limit": 10,
	}, &beforeDeleteCMD)
	require.NoError(t, err, node.DumpDiagnostics())
	require.Contains(t, clientMessageNumbers(beforeDeleteCMD), initialCMDNo)
	ordinaryMutationsBefore := membershipMutationRows(t, ctx, node, "ordinary")
	cmdMutationsBefore := membershipMutationRows(t, ctx, node, "cmd")

	_, err = suite.PostJSON(ctx, "http://"+node.APIAddr()+"/channel/delete", map[string]any{
		"channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
	}, nil)
	require.NoError(t, err, node.DumpDiagnostics())

	tests := []struct {
		name     string
		fromUID  string
		deviceID string
		syncOnce bool
	}{
		{name: "ordinary subscriber", fromUID: memberUID},
		{name: "system UID bypass", fromUID: systemUID},
		{name: "system device command bypass", fromUID: nonmemberUID, deviceID: systemDevice, syncOnce: true},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := suite.PostMessageSend(ctx, node.APIAddr(), sendBody(
				tt.fromUID, channelID, fmt.Sprintf("terminal-after-delete-%d", index), tt.deviceID, tt.syncOnce,
			))
			require.NoError(t, err, node.DumpDiagnostics())
			require.Equal(t, uint8(frame.ReasonDisband), response.Reason, node.DumpDiagnostics())
			require.Zero(t, response.MessageID, node.DumpDiagnostics())
			require.Zero(t, response.MessageSeq, node.DumpDiagnostics())
		})
	}

	page, err := suite.PostConversationList(ctx, node.APIAddr(), memberUID, 10)
	require.NoError(t, err, node.DumpDiagnostics())
	_, found := suite.FindConversationKey(page.Deletes, channelID, int64(frame.ChannelTypeGroup))
	require.True(t, found, "conversation page must surface the terminal channel as a delete: %+v", page)
	_, found = suite.FindConversation(page, channelID)
	require.False(t, found, "terminal channel must not remain in active conversations: %+v", page)

	_, err = suite.PostJSON(ctx, "http://"+node.APIAddr()+"/channel/messagesync", map[string]any{
		"login_uid": memberUID, "channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
		"start_message_seq": 1, "limit": 10, "pull_mode": 0,
	}, nil)
	var statusErr *suite.HTTPStatusError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, http.StatusBadRequest, statusErr.StatusCode)
	require.Contains(t, strings.ToLower(statusErr.Body), "channel disbanded")

	_, err = suite.PostJSON(ctx, "http://"+node.APIAddr()+"/message/sync", map[string]any{
		"uid": memberUID, "message_seq": 0, "limit": 10,
	}, nil)
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, http.StatusBadRequest, statusErr.StatusCode)
	require.Contains(t, strings.ToLower(statusErr.Body), "channel disbanded")
	require.Equal(t, ordinaryMutationsBefore, membershipMutationRows(t, ctx, node, "ordinary"), "disband must not fan out ordinary membership mutations")
	require.Equal(t, cmdMutationsBefore, membershipMutationRows(t, ctx, node, "cmd"), "disband must not fan out CMD membership mutations")
}

func clientMessageNumbers(messages []struct {
	ClientMsgNo string `json:"client_msg_no"`
}) []string {
	values := make([]string, len(messages))
	for index, message := range messages {
		values[index] = message.ClientMsgNo
	}
	return values
}

func membershipMutationRows(t *testing.T, ctx context.Context, node *suite.StartedNode, directory string) float64 {
	t.Helper()
	samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
	require.NoError(t, err, node.DumpDiagnostics())
	return suite.SumMetricSamples(samples, "wukongim_conversation_membership_mutation_rows_total", map[string]string{"directory": directory})
}

func sendBody(fromUID, channelID, clientMsgNo, deviceID string, syncOnce bool) map[string]any {
	body := map[string]any{
		"from_uid": fromUID, "device_id": deviceID,
		"channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
		"client_msg_no": clientMsgNo,
		"payload":       base64.StdEncoding.EncodeToString([]byte(clientMsgNo)),
	}
	if syncOnce {
		body["sync_once"] = 1
	}
	return body
}
