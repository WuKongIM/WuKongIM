//go:build e2e

package expired_leader_lease

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const (
	managerPollInterval = 25 * time.Millisecond
	nearExpiryWindow    = 500 * time.Millisecond
)

type channelRuntimeMetaDetail struct {
	ChannelID    string `json:"channel_id"`
	ChannelType  int64  `json:"channel_type"`
	Leader       uint64 `json:"leader"`
	ChannelEpoch uint64 `json:"channel_epoch"`
	LeaderEpoch  uint64 `json:"leader_epoch"`
	LeaseUntilMS int64  `json:"lease_until_ms"`
}

func TestExpiredLeaderLeaseStillDeliversCrossNodeMessage(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	topology, err := cluster.ResolveSlotTopology(ctx, 1)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Len(t, topology.FollowerNodeIDs, 2)

	senderNode := cluster.MustNode(topology.FollowerNodeIDs[0])
	recipientNode := cluster.MustNode(topology.FollowerNodeIDs[1])
	observerNode := cluster.MustNode(topology.LeaderNodeID)

	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = sender.Close() }()

	recipient, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = recipient.Close() }()

	require.NoError(t, sender.Connect(senderNode.GatewayAddr(), "ooo1", "ooo1-device"))
	require.NoError(t, recipient.Connect(recipientNode.GatewayAddr(), "ooo2", "ooo2-device"))

	ok, err := suite.ConnectionsContainUID(ctx, *senderNode, "ooo1")
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, cluster.DumpDiagnostics())

	ok, err = suite.ConnectionsContainUID(ctx, *recipientNode, "ooo2")
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, cluster.DumpDiagnostics())

	sendAndRequireCrossNodeRecv(
		t,
		cluster,
		sender,
		recipient,
		"ooo1",
		"ooo2",
		"expired-lease-bootstrap-1",
		1,
		[]byte("hello before lease expiry"),
		"",
	)

	channelID := encodePersonChannel("ooo1", "ooo2")
	require.NoError(t, transferSlotLeader(ctx, *senderNode, 1, recipientNode.Spec.ID))
	newTopology, err := cluster.WaitForSlotLeaderChange(ctx, 1, topology.LeaderNodeID)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, recipientNode.Spec.ID, newTopology.LeaderNodeID, cluster.DumpDiagnostics())

	nearlyExpiredMeta, nearlyExpiredBody := waitForNearlyExpiredChannelRuntimeMeta(
		t,
		ctx,
		cluster,
		*cluster.MustNode(newTopology.LeaderNodeID),
		channelID,
		frame.ChannelTypePerson,
		topology.LeaderNodeID,
		nearExpiryWindow,
	)
	nearExpiryRemainingMS := nearlyExpiredMeta.LeaseUntilMS - time.Now().UnixMilli()
	require.Greater(t, nearExpiryRemainingMS, int64(0), cluster.DumpDiagnostics()+"\nchannel runtime meta: "+nearlyExpiredBody)
	t.Logf("observed nearly expired channel runtime metadata: leader=%d channel_epoch=%d leader_epoch=%d lease_remaining_ms=%d body=%s",
		nearlyExpiredMeta.Leader, nearlyExpiredMeta.ChannelEpoch, nearlyExpiredMeta.LeaderEpoch, nearExpiryRemainingMS, nearlyExpiredBody)

	require.NoError(t, observerNode.Stop())

	expiredMeta, expiredBody := waitForExpiredChannelRuntimeMeta(
		t,
		ctx,
		cluster,
		*cluster.MustNode(newTopology.LeaderNodeID),
		channelID,
		frame.ChannelTypePerson,
		topology.LeaderNodeID,
	)
	leaseRemainingMS := expiredMeta.LeaseUntilMS - time.Now().UnixMilli()
	require.Less(t, leaseRemainingMS, int64(0), cluster.DumpDiagnostics()+"\nchannel runtime meta: "+expiredBody)
	t.Logf("observed expired stale channel runtime metadata after old leader stop: leader=%d channel_epoch=%d leader_epoch=%d lease_remaining_ms=%d body=%s",
		expiredMeta.Leader, expiredMeta.ChannelEpoch, expiredMeta.LeaderEpoch, leaseRemainingMS, expiredBody)

	sendAndRequireCrossNodeRecv(
		t,
		cluster,
		sender,
		recipient,
		"ooo1",
		"ooo2",
		"expired-lease-after-expiry-2",
		2,
		[]byte("hello after lease expiry"),
		expiredBody,
	)
}

func sendAndRequireCrossNodeRecv(
	t *testing.T,
	cluster *suite.StartedCluster,
	sender, recipient *suite.WKProtoClient,
	senderUID, recipientUID, clientMsgNo string,
	clientSeq uint64,
	payload []byte,
	channelMetaBody string,
) {
	t.Helper()

	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     payload,
	}), clusterDiagnostics(cluster, channelMetaBody))

	sendack, err := sender.ReadSendAck()
	require.NoError(t, err, clusterDiagnostics(cluster, channelMetaBody))
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)

	recv, err := recipient.ReadRecv()
	require.NoError(t, err, clusterDiagnostics(cluster, channelMetaBody))
	require.Equal(t, senderUID, recv.FromUID)
	require.Equal(t, senderUID, recv.ChannelID)
	require.Equal(t, frame.ChannelTypePerson, recv.ChannelType)
	require.Equal(t, payload, recv.Payload)
	require.Equal(t, sendack.MessageID, recv.MessageID)
	require.Equal(t, sendack.MessageSeq, recv.MessageSeq)

	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq), clusterDiagnostics(cluster, channelMetaBody))
}

func waitForExpiredChannelRuntimeMeta(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	node suite.StartedNode,
	channelID string,
	channelType uint8,
	expectedLeader uint64,
) (channelRuntimeMetaDetail, string) {
	t.Helper()

	return waitForChannelRuntimeMeta(
		t,
		ctx,
		cluster,
		node,
		channelID,
		channelType,
		func(detail channelRuntimeMetaDetail, nowMS int64) (bool, string) {
			if detail.Leader != expectedLeader {
				return false, fmt.Sprintf("leader=%d want=%d", detail.Leader, expectedLeader)
			}
			if detail.LeaseUntilMS >= nowMS {
				return false, fmt.Sprintf("lease_until_ms=%d now_ms=%d", detail.LeaseUntilMS, nowMS)
			}
			return true, ""
		},
		"expired channel runtime metadata that still points to the previous leader",
	)
}

func waitForNearlyExpiredChannelRuntimeMeta(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	node suite.StartedNode,
	channelID string,
	channelType uint8,
	expectedLeader uint64,
	maxRemaining time.Duration,
) (channelRuntimeMetaDetail, string) {
	t.Helper()

	return waitForChannelRuntimeMeta(
		t,
		ctx,
		cluster,
		node,
		channelID,
		channelType,
		func(detail channelRuntimeMetaDetail, nowMS int64) (bool, string) {
			if detail.Leader != expectedLeader {
				return false, fmt.Sprintf("leader=%d want=%d", detail.Leader, expectedLeader)
			}
			remainingMS := detail.LeaseUntilMS - nowMS
			if remainingMS <= 0 {
				return false, fmt.Sprintf("lease already expired: lease_until_ms=%d now_ms=%d", detail.LeaseUntilMS, nowMS)
			}
			if remainingMS > maxRemaining.Milliseconds() {
				return false, fmt.Sprintf("lease_remaining_ms=%d want<=%d", remainingMS, maxRemaining.Milliseconds())
			}
			return true, ""
		},
		fmt.Sprintf("nearly expired channel runtime metadata within %s that still points to the previous leader", maxRemaining),
	)
}

func waitForChannelRuntimeMeta(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	node suite.StartedNode,
	channelID string,
	channelType uint8,
	match func(detail channelRuntimeMetaDetail, nowMS int64) (bool, string),
	description string,
) (channelRuntimeMetaDetail, string) {
	t.Helper()

	ticker := time.NewTicker(managerPollInterval)
	defer ticker.Stop()

	var (
		lastBody string
		lastErr  error
	)

	for {
		detail, body, err := fetchChannelRuntimeMetaDetail(ctx, node, channelID, channelType)
		if err == nil {
			lastBody = body
			nowMS := time.Now().UnixMilli()
			if ok, reason := match(detail, nowMS); ok {
				return detail, body
			} else {
				lastErr = fmt.Errorf("%s: %s", description, reason)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			require.NoError(t, lastErr, clusterDiagnostics(cluster, lastBody))
			t.Fatalf("timed out waiting for %s: %s", description, clusterDiagnostics(cluster, lastBody))
		case <-ticker.C:
		}
	}
}

func transferSlotLeader(ctx context.Context, node suite.StartedNode, slotID uint32, targetNodeID uint64) error {
	body, err := json.Marshal(map[string]uint64{
		"target_node_id": targetNodeID,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("http://%s/manager/slots/%d/leader/transfer", node.Spec.ManagerAddr, slotID),
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slot leader transfer returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func fetchChannelRuntimeMetaDetail(
	ctx context.Context,
	node suite.StartedNode,
	channelID string,
	channelType uint8,
) (channelRuntimeMetaDetail, string, error) {
	path := fmt.Sprintf(
		"http://%s/manager/channel-runtime-meta/%d/%s",
		node.Spec.ManagerAddr,
		channelType,
		url.PathEscape(channelID),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return channelRuntimeMetaDetail{}, "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return channelRuntimeMetaDetail{}, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return channelRuntimeMetaDetail{}, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return channelRuntimeMetaDetail{}, string(body), fmt.Errorf("manager channel runtime meta returned %d: %s", resp.StatusCode, string(body))
	}

	var detail channelRuntimeMetaDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return channelRuntimeMetaDetail{}, string(body), err
	}
	return detail, string(body), nil
}

func encodePersonChannel(leftUID, rightUID string) string {
	leftHash := crc32.ChecksumIEEE([]byte(leftUID))
	rightHash := crc32.ChecksumIEEE([]byte(rightUID))
	if leftHash > rightHash {
		return leftUID + "@" + rightUID
	}
	if leftHash == rightHash && leftUID > rightUID {
		return leftUID + "@" + rightUID
	}
	return rightUID + "@" + leftUID
}

func clusterDiagnostics(cluster *suite.StartedCluster, channelMetaBody string) string {
	if channelMetaBody == "" {
		return cluster.DumpDiagnostics()
	}
	return cluster.DumpDiagnostics() + "\nchannel runtime meta body: " + channelMetaBody
}
