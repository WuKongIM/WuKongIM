package workload

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestGroupPrepareHugeOwnerCreatesChannelThenWaitsAndAddsSubscribers(t *testing.T) {
	target := newRecordingGroupPrepareTarget()
	barrier := &recordingGroupPrepareBarrier{target: target}

	err := PrepareGroup(context.Background(), GroupPrepareConfig{
		RunID:                "bench-run",
		WorkerID:             "a",
		ProfileName:          "huge-group",
		ShardMode:            model.ShardModeSplitMembersAndTraffic,
		OwnsChannel:          true,
		ChannelRange:         model.Range{Start: 0, End: 1},
		MemberRange:          model.Range{Start: 0, End: 2500},
		SubscribersBatchSize: 2500,
		UIDPrefix:            "bench-u",
	}, target, barrier)

	require.NoError(t, err)
	require.Equal(t, []string{"channels:bench-run-channels-huge-group-a-0-1", "barrier:channel_prepared", "subs:bench-run-subs-huge-group-a-0-2500"}, target.events)
	require.Len(t, target.channelRequests, 1)
	require.Equal(t, model.BatchChannelsRequest{
		RunID:   "bench-run",
		BatchID: "bench-run-channels-huge-group-a-0-1",
		Upsert:  true,
		Channels: []model.ChannelItem{{
			ChannelID:   "bench-run-huge-group-0",
			ChannelType: uint8(frame.ChannelTypeGroup),
			Large:       true,
		}},
	}, target.channelRequests[0])
	require.Equal(t, []groupBarrierWait{{runID: "bench-run", name: "channel_prepared"}}, barrier.waits)
	require.Len(t, target.subscriberRequests, 1)
	require.Equal(t, "bench-run-subs-huge-group-a-0-2500", target.subscriberRequests[0].BatchID)
	require.False(t, target.subscriberRequests[0].Items[0].Reset)
	require.Equal(t, []string{"bench-u-0", "bench-u-1", "bench-u-2499"}, []string{
		target.subscriberRequests[0].Items[0].Subscribers[0],
		target.subscriberRequests[0].Items[0].Subscribers[1],
		target.subscriberRequests[0].Items[0].Subscribers[2499],
	})
}

func TestGroupPrepareHugeNonOwnerWaitsBeforeChunkedSubscriberAdds(t *testing.T) {
	target := newRecordingGroupPrepareTarget()
	barrier := &recordingGroupPrepareBarrier{target: target}

	err := PrepareGroup(context.Background(), GroupPrepareConfig{
		RunID:                "bench-run",
		WorkerID:             "b",
		ProfileName:          "huge-group",
		ShardMode:            model.ShardModeSplitMembersAndTraffic,
		OwnsChannel:          false,
		ChannelRange:         model.Range{Start: 0, End: 1},
		MemberRange:          model.Range{Start: 2500, End: 2505},
		SubscribersBatchSize: 2,
		UIDPrefix:            "bench-u",
	}, target, barrier)

	require.NoError(t, err)
	require.Empty(t, target.channelRequests)
	require.Equal(t, []string{"barrier:channel_prepared", "subs:bench-run-subs-huge-group-b-2500-2502", "subs:bench-run-subs-huge-group-b-2502-2504", "subs:bench-run-subs-huge-group-b-2504-2505"}, target.events)
	require.Len(t, target.subscriberRequests, 3)
	for _, req := range target.subscriberRequests {
		require.Len(t, req.Items, 1)
		require.Equal(t, "bench-run-huge-group-0", req.Items[0].ChannelID)
		require.Equal(t, uint8(frame.ChannelTypeGroup), req.Items[0].ChannelType)
		require.False(t, req.Items[0].Reset)
		require.LessOrEqual(t, len(req.Items[0].Subscribers), 2)
	}
}

func TestGroupPrepareSmallGroupsCreatesOwnedChannelsAndSubscriberBatches(t *testing.T) {
	target := newRecordingGroupPrepareTarget()

	err := PrepareGroup(context.Background(), GroupPrepareConfig{
		RunID:                "run-small",
		WorkerID:             "worker-a",
		ProfileName:          "small-group",
		ChannelRange:         model.Range{Start: 2, End: 4},
		MembersPerChannel:    3,
		SubscribersBatchSize: 2,
		UIDPrefix:            "u",
	}, target, NoopGroupPrepareBarrier{})

	require.NoError(t, err)
	require.Len(t, target.channelRequests, 1)
	require.Equal(t, []model.ChannelItem{
		{ChannelID: "run-small-small-group-2", ChannelType: uint8(frame.ChannelTypeGroup)},
		{ChannelID: "run-small-small-group-3", ChannelType: uint8(frame.ChannelTypeGroup)},
	}, target.channelRequests[0].Channels)
	require.Len(t, target.subscriberRequests, 4)
	require.Equal(t, []string{"u-6", "u-7"}, target.subscriberRequests[0].Items[0].Subscribers)
	require.Equal(t, []string{"u-8"}, target.subscriberRequests[1].Items[0].Subscribers)
	require.Equal(t, []string{"u-9", "u-10"}, target.subscriberRequests[2].Items[0].Subscribers)
	require.Equal(t, []string{"u-11"}, target.subscriberRequests[3].Items[0].Subscribers)
}

func TestGroupWorkloadLocalRateUsesOwnedPartitionShare(t *testing.T) {
	got := GroupLocalRate(model.Rate{PerSecond: 20}, 4, []int{2})

	require.InDelta(t, 5.0, got.PerSecond, 0.001)
}

func TestGroupWorkloadSendOneBuildsGroupSendAndVerifiesFullReceivers(t *testing.T) {
	clients := map[string]PersonClient{
		"u-0": newRecordingPersonClient(),
		"u-1": newRecordingPersonClient(),
		"u-2": newRecordingPersonClient(),
	}
	clientMsgNo := "bench-msg-run-a-huge-group-group-send-ch0-msg0"
	clients["u-0"].(*recordingPersonClient).sendacks = append(clients["u-0"].(*recordingPersonClient).sendacks, &frame.SendackPacket{
		MessageID:   99,
		MessageSeq:  7,
		ClientSeq:   0,
		ClientMsgNo: clientMsgNo,
		ReasonCode:  frame.ReasonSuccess,
	})
	for uid, client := range clients {
		client.(*recordingPersonClient).recvFrames = append(client.(*recordingPersonClient).recvFrames, groupRecv(clientMsgNo, uid, 99, 7))
	}
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "huge-group",
		TrafficName:     "group-send",
		ClientMsgPrefix: "bench-msg",
		VerifyRecvMode:  "full",
		RecvAck:         true,
		Channels: []GroupChannel{{
			ChannelIndex:   0,
			ChannelID:      "run-a-huge-group-0",
			OnlineMembers:  []string{"u-0", "u-1", "u-2"},
			TrafficIndexes: []int{0},
		}},
		Metrics: metrics.NewRegistry(),
	}, clients)
	require.NoError(t, err)

	require.NoError(t, workload.Run(context.Background()))

	sender := clients["u-0"].(*recordingPersonClient)
	require.Len(t, sender.sentFrames, 1)
	require.Equal(t, "run-a-huge-group-0", sender.sentFrames[0].ChannelID)
	require.Equal(t, frame.ChannelTypeGroup, sender.sentFrames[0].ChannelType)
	require.Equal(t, uint64(1), workload.Metrics().CounterValue("group_send_success_total", nil))
	require.Equal(t, uint64(3), workload.Metrics().CounterValue("group_recv_success_total", nil))
	for uid, client := range clients {
		require.Len(t, client.(*recordingPersonClient).recvAckCalls, 1, uid)
	}
}

func TestGroupWorkloadSampledRecvVerificationIsDeterministic(t *testing.T) {
	clients := map[string]PersonClient{
		"u-0": newRecordingPersonClient(),
		"u-1": newRecordingPersonClient(),
		"u-2": newRecordingPersonClient(),
		"u-3": newRecordingPersonClient(),
	}
	clientMsgNo := "bench-msg-run-a-huge-group-group-send-ch0-msg0"
	clients["u-0"].(*recordingPersonClient).sendacks = append(clients["u-0"].(*recordingPersonClient).sendacks, &frame.SendackPacket{
		MessageID:   100,
		MessageSeq:  8,
		ClientSeq:   0,
		ClientMsgNo: clientMsgNo,
		ReasonCode:  frame.ReasonSuccess,
	})
	for _, uid := range []string{"u-0", "u-1"} {
		clients[uid].(*recordingPersonClient).recvFrames = append(clients[uid].(*recordingPersonClient).recvFrames, groupRecv(clientMsgNo, uid, 100, 8))
	}
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:                  "run-a",
		ProfileName:            "huge-group",
		TrafficName:            "group-send",
		ClientMsgPrefix:        "bench-msg",
		VerifyRecvMode:         "sampled",
		RecvSampleSize:         2,
		RecvAck:                true,
		OwnedTrafficPartitions: []int{0},
		TrafficPartitionCount:  4,
		Channels: []GroupChannel{{
			ChannelIndex:  0,
			ChannelID:     "run-a-huge-group-0",
			OnlineMembers: []string{"u-0", "u-1", "u-2", "u-3"},
		}},
		Metrics: metrics.NewRegistry(),
	}, clients)
	require.NoError(t, err)

	require.NoError(t, workload.Run(context.Background()))

	require.Len(t, clients["u-0"].(*recordingPersonClient).recvAckCalls, 1)
	require.Len(t, clients["u-1"].(*recordingPersonClient).recvAckCalls, 1)
	require.Empty(t, clients["u-2"].(*recordingPersonClient).recvAckCalls)
	require.Empty(t, clients["u-3"].(*recordingPersonClient).recvAckCalls)
	require.Equal(t, uint64(2), workload.Metrics().CounterValue("group_recv_success_total", nil))
}

func groupRecv(clientMsgNo, recipientUID string, messageID int64, messageSeq uint64) *frame.RecvPacket {
	return &frame.RecvPacket{
		MessageID:   messageID,
		MessageSeq:  messageSeq,
		FromUID:     "u-0",
		ChannelID:   "run-a-huge-group-0",
		ChannelType: frame.ChannelTypeGroup,
		ClientMsgNo: clientMsgNo,
		Payload:     []byte("run=run-a profile=huge-group traffic=group-send channel=0 message=0"),
	}
}

type recordingGroupPrepareTarget struct {
	mu                 sync.Mutex
	events             []string
	channelRequests    []model.BatchChannelsRequest
	subscriberRequests []model.BatchSubscribersRequest
}

func newRecordingGroupPrepareTarget() *recordingGroupPrepareTarget {
	return &recordingGroupPrepareTarget{}
}

func (t *recordingGroupPrepareTarget) UpsertChannels(ctx context.Context, req model.BatchChannelsRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, fmt.Sprintf("channels:%s", req.BatchID))
	t.channelRequests = append(t.channelRequests, req)
	return nil
}

func (t *recordingGroupPrepareTarget) AddSubscribers(ctx context.Context, req model.BatchSubscribersRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, fmt.Sprintf("subs:%s", req.BatchID))
	t.subscriberRequests = append(t.subscriberRequests, req)
	return nil
}

type recordingGroupPrepareBarrier struct {
	target *recordingGroupPrepareTarget
	waits  []groupBarrierWait
}

type groupBarrierWait struct {
	runID string
	name  string
}

func (b *recordingGroupPrepareBarrier) Wait(ctx context.Context, runID, name string) error {
	b.waits = append(b.waits, groupBarrierWait{runID: runID, name: name})
	if b.target != nil {
		b.target.mu.Lock()
		b.target.events = append(b.target.events, fmt.Sprintf("barrier:%s", name))
		b.target.mu.Unlock()
	}
	return nil
}
