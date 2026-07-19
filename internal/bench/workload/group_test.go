package workload

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
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
	require.Equal(t, []string{
		"channels:bench-run-channels-huge-group-a-0-1",
		"barrier:channel_prepared",
		"subs:bench-run-subs-huge-group-a-0-1000",
		"subs:bench-run-subs-huge-group-a-1000-2000",
		"subs:bench-run-subs-huge-group-a-2000-2500",
	}, target.events)
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
	require.Len(t, target.subscriberRequests, 3)
	require.Equal(t, "bench-run-subs-huge-group-a-0-1000", target.subscriberRequests[0].BatchID)
	require.Equal(t, "bench-run-subs-huge-group-a-1000-2000", target.subscriberRequests[1].BatchID)
	require.Equal(t, "bench-run-subs-huge-group-a-2000-2500", target.subscriberRequests[2].BatchID)
	for _, req := range target.subscriberRequests {
		require.Len(t, req.Items, 1)
		require.False(t, req.Items[0].Reset)
		require.LessOrEqual(t, len(req.Items[0].Subscribers), 1000)
	}
	require.Equal(t, []string{"bench-u-0", "bench-u-1", "bench-u-999"}, []string{
		target.subscriberRequests[0].Items[0].Subscribers[0],
		target.subscriberRequests[0].Items[0].Subscribers[1],
		target.subscriberRequests[0].Items[0].Subscribers[999],
	})
	require.Equal(t, []string{"bench-u-1000", "bench-u-1001", "bench-u-1999"}, []string{
		target.subscriberRequests[1].Items[0].Subscribers[0],
		target.subscriberRequests[1].Items[0].Subscribers[1],
		target.subscriberRequests[1].Items[0].Subscribers[999],
	})
	require.Equal(t, []string{"bench-u-2000", "bench-u-2001", "bench-u-2499"}, []string{
		target.subscriberRequests[2].Items[0].Subscribers[0],
		target.subscriberRequests[2].Items[0].Subscribers[1],
		target.subscriberRequests[2].Items[0].Subscribers[499],
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
	require.Len(t, target.subscriberRequests, 1)
	require.Equal(t, "run-small-subs-small-group-worker-a-2-4", target.subscriberRequests[0].BatchID)
	require.Len(t, target.subscriberRequests[0].Items, 4)
	require.Equal(t, []string{"u-6", "u-7"}, target.subscriberRequests[0].Items[0].Subscribers)
	require.Equal(t, []string{"u-8"}, target.subscriberRequests[0].Items[1].Subscribers)
	require.Equal(t, []string{"u-9", "u-10"}, target.subscriberRequests[0].Items[2].Subscribers)
	require.Equal(t, []string{"u-11"}, target.subscriberRequests[0].Items[3].Subscribers)
}

func TestGroupPrepareAllowedOverlapUsesSharedMemberPool(t *testing.T) {
	target := newRecordingGroupPrepareTarget()

	err := PrepareGroup(context.Background(), GroupPrepareConfig{
		RunID:             "run-overlap",
		WorkerID:          "worker-a",
		ProfileName:       "small-group",
		ChannelRange:      model.Range{Start: 1, End: 2},
		MemberRange:       model.Range{Start: 0, End: 100},
		MembersPerChannel: 60,
		MemberReusePolicy: "allowed",
		UIDPrefix:         "u",
	}, target, NoopGroupPrepareBarrier{})

	require.NoError(t, err)
	var subscribers []string
	for _, req := range target.subscriberRequests {
		require.Len(t, req.Items, 1)
		subscribers = append(subscribers, req.Items[0].Subscribers...)
	}
	require.Len(t, subscribers, 60)
	require.NotContains(t, subscribers, "u-119")
	for _, uid := range subscribers {
		require.NotRegexp(t, `^u-1[0-9][0-9]$`, uid)
	}
}

func TestGroupPrepareChunksChannelUpsertsAtThousand(t *testing.T) {
	target := newRecordingGroupPrepareTarget()

	err := PrepareGroup(context.Background(), GroupPrepareConfig{
		RunID:             "run-large",
		WorkerID:          "worker-a",
		ProfileName:       "large-group",
		ChannelRange:      model.Range{Start: 0, End: 2500},
		MembersPerChannel: 0,
		UIDPrefix:         "u",
	}, target, NoopGroupPrepareBarrier{})

	require.NoError(t, err)
	require.Len(t, target.channelRequests, 3)
	require.Equal(t, []string{
		"run-large-channels-large-group-worker-a-0-1000",
		"run-large-channels-large-group-worker-a-1000-2000",
		"run-large-channels-large-group-worker-a-2000-2500",
	}, []string{
		target.channelRequests[0].BatchID,
		target.channelRequests[1].BatchID,
		target.channelRequests[2].BatchID,
	})
	require.Len(t, target.channelRequests[0].Channels, 1000)
	require.Len(t, target.channelRequests[1].Channels, 1000)
	require.Len(t, target.channelRequests[2].Channels, 500)
	require.Equal(t, "run-large-large-group-0", target.channelRequests[0].Channels[0].ChannelID)
	require.Equal(t, "run-large-large-group-999", target.channelRequests[0].Channels[999].ChannelID)
	require.Equal(t, "run-large-large-group-1000", target.channelRequests[1].Channels[0].ChannelID)
	require.Equal(t, "run-large-large-group-1999", target.channelRequests[1].Channels[999].ChannelID)
	require.Equal(t, "run-large-large-group-2000", target.channelRequests[2].Channels[0].ChannelID)
	require.Equal(t, "run-large-large-group-2499", target.channelRequests[2].Channels[499].ChannelID)
}

func TestGroupPrepareBatchesSmallGroupSubscriberItemsAtThousand(t *testing.T) {
	target := newRecordingGroupPrepareTarget()

	err := PrepareGroup(context.Background(), GroupPrepareConfig{
		RunID:             "run-large",
		WorkerID:          "worker-a",
		ProfileName:       "small-group",
		ChannelRange:      model.Range{Start: 0, End: 2500},
		MembersPerChannel: 1,
		UIDPrefix:         "u",
	}, target, NoopGroupPrepareBarrier{})

	require.NoError(t, err)
	require.Len(t, target.subscriberRequests, 3)
	require.Equal(t, []string{
		"run-large-subs-small-group-worker-a-0-1000",
		"run-large-subs-small-group-worker-a-1000-2000",
		"run-large-subs-small-group-worker-a-2000-2500",
	}, []string{
		target.subscriberRequests[0].BatchID,
		target.subscriberRequests[1].BatchID,
		target.subscriberRequests[2].BatchID,
	})
	require.Len(t, target.subscriberRequests[0].Items, 1000)
	require.Len(t, target.subscriberRequests[1].Items, 1000)
	require.Len(t, target.subscriberRequests[2].Items, 500)
	require.Equal(t, "run-large-small-group-0", target.subscriberRequests[0].Items[0].ChannelID)
	require.Equal(t, "run-large-small-group-999", target.subscriberRequests[0].Items[999].ChannelID)
	require.Equal(t, "run-large-small-group-1000", target.subscriberRequests[1].Items[0].ChannelID)
	require.Equal(t, "run-large-small-group-2499", target.subscriberRequests[2].Items[499].ChannelID)
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
	clientMsgNo := "bench-msg-run-a-huge-group-group-send-run-ch0-msg0"
	clients["u-0"].(*recordingPersonClient).sendacks = append(clients["u-0"].(*recordingPersonClient).sendacks, &frame.SendackPacket{
		MessageID:   99,
		MessageSeq:  7,
		ClientSeq:   0,
		ClientMsgNo: clientMsgNo,
		ReasonCode:  frame.ReasonSuccess,
	})
	for _, uid := range []string{"u-1", "u-2"} {
		clients[uid].(*recordingPersonClient).recvFrames = append(clients[uid].(*recordingPersonClient).recvFrames, groupRecv(clientMsgNo, uid, 99, 7))
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
	sendLabels := groupSendLabels("run", "huge-group", "group-send")
	require.Equal(t, uint64(1), workload.Metrics().CounterValue("group_send_success_total", sendLabels))
	require.Equal(t, uint64(2), workload.Metrics().CounterValue("group_recv_success_total", sendLabels))
	require.Empty(t, clients["u-0"].(*recordingPersonClient).recvAckCalls)
	require.Len(t, clients["u-1"].(*recordingPersonClient).recvAckCalls, 1)
	require.Len(t, clients["u-2"].(*recordingPersonClient).recvAckCalls, 1)
}

func TestGroupWorkloadRecvMetricsAreSeparatedByPhase(t *testing.T) {
	sender := newRecordingPersonClient()
	sender.autoSendack = true
	recipient := newRecordingPersonClient()
	for messageIndex, phase := range []string{"warmup", "run"} {
		clientMsgNo := fmt.Sprintf("bench-msg-run-a-group-profile-group-send-%s-ch0-msg%d", phase, messageIndex)
		recipient.recvFrames = append(recipient.recvFrames, &frame.RecvPacket{
			FromUID:     "u-0",
			ChannelID:   "run-a-group-profile-0",
			ChannelType: frame.ChannelTypeGroup,
			ClientMsgNo: clientMsgNo,
			Payload:     []byte(fmt.Sprintf("run=run-a profile=group-profile traffic=group-send phase=%s channel=0 message=%d", phase, messageIndex)),
		})
	}
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "group-profile",
		TrafficName:     "group-send",
		ClientMsgPrefix: "bench-msg",
		VerifyRecvMode:  "full",
		Channels: []GroupChannel{{
			ChannelIndex:  0,
			ChannelID:     "run-a-group-profile-0",
			OnlineMembers: []string{"u-0", "u-1"},
		}},
		Metrics: metrics.NewRegistry(),
	}, map[string]PersonClient{"u-0": sender, "u-1": recipient})
	require.NoError(t, err)

	require.NoError(t, workload.sendOneInPhase(context.Background(), "warmup", 0, 0))
	require.NoError(t, workload.sendOneInPhase(context.Background(), "run", 0, 1))

	for _, phase := range []string{"warmup", "run"} {
		labels := groupSendLabels(phase, "group-profile", "group-send")
		require.Equal(t, uint64(1), workload.Metrics().CounterValue("group_recv_success_total", labels))
		require.NotEmpty(t, workload.Metrics().LatencyValues("group_recv_latency_seconds", labels))
	}
	require.Zero(t, workload.Metrics().CounterValue("group_recv_success_total", nil))
	require.Empty(t, workload.Metrics().LatencyValues("group_recv_latency_seconds", nil))
}

func TestGroupWorkloadRecvErrorUsesPhaseLabels(t *testing.T) {
	sender := newRecordingPersonClient()
	sender.autoSendack = true
	recipient := newRecordingPersonClient()
	recipient.recvFrames = append(recipient.recvFrames, &frame.RecvPacket{
		FromUID:     "u-0",
		ChannelID:   "run-a-group-profile-0",
		ChannelType: frame.ChannelTypeGroup,
		ClientMsgNo: "bench-msg-run-a-group-profile-group-send-warmup-ch0-msg0",
		Payload:     []byte("wrong payload"),
	})
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "group-profile",
		TrafficName:     "group-send",
		ClientMsgPrefix: "bench-msg",
		VerifyRecvMode:  "full",
		Channels: []GroupChannel{{
			ChannelIndex:  0,
			ChannelID:     "run-a-group-profile-0",
			OnlineMembers: []string{"u-0", "u-1"},
		}},
		Metrics: metrics.NewRegistry(),
	}, map[string]PersonClient{"u-0": sender, "u-1": recipient})
	require.NoError(t, err)

	require.Error(t, workload.sendOneInPhase(context.Background(), "warmup", 0, 0))
	require.Equal(t, uint64(1), workload.Metrics().CounterValue("group_recv_error_total", groupSendLabels("warmup", "group-profile", "group-send")))
	require.Zero(t, workload.Metrics().CounterValue("group_recv_error_total", nil))
}

func TestGroupWorkloadSendOneRejectsSendackWithoutExpectedClientMsgNo(t *testing.T) {
	clients := map[string]PersonClient{
		"u-0": newRecordingPersonClient(),
		"u-1": newRecordingPersonClient(),
	}
	clients["u-0"].(*recordingPersonClient).sendacks = append(clients["u-0"].(*recordingPersonClient).sendacks, &frame.SendackPacket{
		MessageID:  99,
		MessageSeq: 7,
		ClientSeq:  0,
		ReasonCode: frame.ReasonSuccess,
	})
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "huge-group",
		TrafficName:     "group-send",
		ClientMsgPrefix: "bench-msg",
		Channels: []GroupChannel{{
			ChannelIndex:   0,
			ChannelID:      "run-a-huge-group-0",
			OnlineMembers:  []string{"u-0", "u-1"},
			TrafficIndexes: []int{0},
		}},
		Metrics: metrics.NewRegistry(),
	}, clients)
	require.NoError(t, err)

	err = workload.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "sendack not received")
}

func TestGroupWorkloadMeasuredRunKeepsGoingAfterOperationError(t *testing.T) {
	sender := newRecordingPersonClient()
	sender.autoSendack = true
	sender.readErrors = append(sender.readErrors, context.DeadlineExceeded)
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "group-profile",
		TrafficName:     "group-send",
		ClientMsgPrefix: "bench-msg",
		RunDuration:     50 * time.Millisecond,
		LocalRate:       model.Rate{PerSecond: 100},
		MaxConcurrency:  4,
		Channels: []GroupChannel{{
			ChannelIndex:  0,
			ChannelID:     "run-a-group-profile-0",
			OnlineMembers: []string{"u-0"},
		}},
		Metrics: metrics.NewRegistry(),
	}, map[string]PersonClient{"u-0": sender})
	require.NoError(t, err)

	require.NoError(t, workload.Run(context.Background()))

	labels := groupSendLabels("run", "group-profile", "group-send")
	require.Len(t, sender.sentFrames, 5)
	require.Equal(t, uint64(1), workload.Metrics().CounterValue("group_send_error_total", labels))
	require.Equal(t, uint64(4), workload.Metrics().CounterValue("group_send_success_total", labels))
}

func TestGroupWorkloadWarmupKeepsGoingAfterOperationError(t *testing.T) {
	sender := newRecordingPersonClient()
	sender.autoSendack = true
	sender.readErrors = append(sender.readErrors, context.DeadlineExceeded)
	channels := make([]GroupChannel, 2)
	for i := range channels {
		channels[i] = GroupChannel{
			ChannelIndex:  i,
			ChannelID:     fmt.Sprintf("run-a-group-profile-%d", i),
			OnlineMembers: []string{"u-0"},
		}
	}
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "group-profile",
		TrafficName:     "group-send",
		ClientMsgPrefix: "bench-msg",
		WarmupDuration:  50 * time.Millisecond,
		LocalRate:       model.Rate{PerSecond: 100},
		MaxConcurrency:  1,
		Channels:        channels,
		Metrics:         metrics.NewRegistry(),
	}, map[string]PersonClient{"u-0": sender})
	require.NoError(t, err)

	require.NoError(t, workload.Warmup(context.Background()))

	labels := groupSendLabels("warmup", "group-profile", "group-send")
	require.Len(t, sender.sentFrames, 2)
	require.Equal(t, uint64(1), workload.Metrics().CounterValue("group_send_error_total", labels))
	require.Equal(t, uint64(1), workload.Metrics().CounterValue("group_send_success_total", labels))
}

func TestGroupWorkloadPipelinesSendackOperationsPerWrappedClient(t *testing.T) {
	raw := newSerialSendackClient()
	clients := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u-0": raw})
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "huge-group",
		TrafficName:     "group-send",
		ClientMsgPrefix: "bench-msg",
		Channels: []GroupChannel{
			{ChannelIndex: 0, ChannelID: "run-a-huge-group-0", OnlineMembers: []string{"u-0"}},
			{ChannelIndex: 1, ChannelID: "run-a-huge-group-1", OnlineMembers: []string{"u-0"}},
		},
		Metrics: metrics.NewRegistry(),
	}, clients)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- workload.SendOne(ctx, 0, 0)
	}()
	require.True(t, raw.waitFirstSend(t, time.Second), "first send did not start")

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- workload.SendOne(ctx, 1, 0)
	}()
	require.True(t, raw.waitSecondSend(time.Second), "second send did not start while first sendack was pending")

	raw.releaseFirstAck()
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	require.Equal(t, []uint64{1, 2}, raw.sentClientSeqs())
}

func TestGroupWorkloadSampledRecvVerificationIsDeterministic(t *testing.T) {
	clients := map[string]PersonClient{
		"u-0": newRecordingPersonClient(),
		"u-1": newRecordingPersonClient(),
		"u-2": newRecordingPersonClient(),
		"u-3": newRecordingPersonClient(),
	}
	clientMsgNo := "bench-msg-run-a-huge-group-group-send-run-ch0-msg0"
	clients["u-0"].(*recordingPersonClient).sendacks = append(clients["u-0"].(*recordingPersonClient).sendacks, &frame.SendackPacket{
		MessageID:   100,
		MessageSeq:  8,
		ClientSeq:   0,
		ClientMsgNo: clientMsgNo,
		ReasonCode:  frame.ReasonSuccess,
	})
	for _, uid := range []string{"u-1"} {
		clients[uid].(*recordingPersonClient).recvFrames = append(clients[uid].(*recordingPersonClient).recvFrames, groupRecv(clientMsgNo, uid, 100, 8))
	}
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:                  "run-a",
		ProfileName:            "huge-group",
		TrafficName:            "group-send",
		ClientMsgPrefix:        "bench-msg",
		VerifyRecvMode:         "sampled",
		RecvSampleSize:         1,
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

	require.Len(t, clients["u-1"].(*recordingPersonClient).recvAckCalls, 1)
	require.Empty(t, clients["u-0"].(*recordingPersonClient).recvAckCalls)
	require.Empty(t, clients["u-2"].(*recordingPersonClient).recvAckCalls)
	require.Empty(t, clients["u-3"].(*recordingPersonClient).recvAckCalls)
	require.Equal(t, uint64(1), workload.Metrics().CounterValue("group_recv_success_total", groupSendLabels("run", "huge-group", "group-send")))
}

func TestGroupWorkloadSendackReadErrorIdentifiesFailedSession(t *testing.T) {
	sender := newRecordingPersonClient()
	sender.readErrors = append(sender.readErrors, context.DeadlineExceeded)
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "group-profile",
		TrafficName:     "group-send",
		ClientMsgPrefix: "bench-msg",
		Channels: []GroupChannel{{
			ChannelIndex:  0,
			ChannelID:     "run-a-group-profile-0",
			OnlineMembers: []string{"u-0"},
		}},
		Metrics: metrics.NewRegistry(),
	}, map[string]PersonClient{"u-0": sender})
	require.NoError(t, err)

	err = workload.SendOne(context.Background(), 0, 1)

	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, []string{"u-0"}, SessionErrorUIDs(err))
	require.Contains(t, err.Error(), "bench-msg-run-a-group-profile-group-send-run-ch0-msg1")
	require.True(t, workload.Metrics().CounterValue("group_send_error_total", groupSendLabels("run", "group-profile", "group-send")) > 0)
	require.NotEmpty(t, workload.Metrics().ErrorSamples())
	require.Contains(t, workload.Metrics().ErrorSamples()[0].Message, "bench-msg-run-a-group-profile-group-send-run-ch0-msg1")
}

func TestGroupWorkloadDoesNotCountPhaseCancellationAsSendError(t *testing.T) {
	sender := newRecordingPersonClient()
	sender.readErrors = append(sender.readErrors, context.Canceled)
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "group-profile",
		TrafficName:     "group-send",
		ClientMsgPrefix: "bench-msg",
		Channels: []GroupChannel{{
			ChannelIndex:  0,
			ChannelID:     "run-a-group-profile-0",
			OnlineMembers: []string{"u-0"},
		}},
		Metrics: metrics.NewRegistry(),
	}, map[string]PersonClient{"u-0": sender})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = workload.SendOne(ctx, 0, 1)

	require.Error(t, err)
	require.Zero(t, workload.Metrics().CounterValue("group_send_error_total", groupSendLabels("run", "group-profile", "group-send")))
	require.Empty(t, workload.Metrics().ErrorSamples())
}

func TestGroupWorkloadDoesNotCountPhaseCancellationAsRecvError(t *testing.T) {
	sender := newRecordingPersonClient()
	sender.autoSendack = true
	recipient := newRecordingPersonClient()
	recipient.readErrors = append(recipient.readErrors, context.Canceled)
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "group-profile",
		TrafficName:     "group-send",
		ClientMsgPrefix: "bench-msg",
		VerifyRecvMode:  "full",
		Channels: []GroupChannel{{
			ChannelIndex:  0,
			ChannelID:     "run-a-group-profile-0",
			OnlineMembers: []string{"u-0", "u-1"},
		}},
		Metrics: metrics.NewRegistry(),
	}, map[string]PersonClient{"u-0": sender, "u-1": recipient})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = workload.SendOne(ctx, 0, 1)

	require.Error(t, err)
	require.Zero(t, workload.Metrics().CounterValue("group_recv_error_total", groupSendLabels("run", "group-profile", "group-send")))
	require.Empty(t, workload.Metrics().ErrorSamples())
}

func TestGroupWorkloadWarmupUsesWarmupDurationAsMinimumAckTimeout(t *testing.T) {
	sender := newDelayedSendackClient(10 * time.Millisecond)
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "group-profile",
		TrafficName:     "group-send",
		ClientMsgPrefix: "bench-msg",
		LocalRate:       model.Rate{PerSecond: 1},
		MaxConcurrency:  1,
		WarmupDuration:  50 * time.Millisecond,
		AckTimeout:      time.Millisecond,
		Channels: []GroupChannel{{
			ChannelIndex:  0,
			ChannelID:     "run-a-group-profile-0",
			OnlineMembers: []string{"u-0"},
		}},
		Metrics: metrics.NewRegistry(),
	}, map[string]PersonClient{"u-0": sender})
	require.NoError(t, err)

	require.NoError(t, workload.Warmup(context.Background()))
	require.Equal(t, uint64(1), workload.Metrics().CounterValue("group_send_success_total", groupSendLabels("warmup", "group-profile", "group-send")))
}

func groupRecv(clientMsgNo, recipientUID string, messageID int64, messageSeq uint64) *frame.RecvPacket {
	return &frame.RecvPacket{
		MessageID:   messageID,
		MessageSeq:  messageSeq,
		FromUID:     "u-0",
		ChannelID:   "run-a-huge-group-0",
		ChannelType: frame.ChannelTypeGroup,
		ClientMsgNo: clientMsgNo,
		Payload:     []byte("run=run-a profile=huge-group traffic=group-send phase=run channel=0 message=0"),
	}
}

type serialSendackClient struct {
	mu               sync.Mutex
	sent             []*frame.SendPacket
	readIndex        int
	firstSend        chan struct{}
	secondSend       chan struct{}
	releaseFirst     chan struct{}
	firstSendClosed  bool
	secondSendClosed bool
}

func newSerialSendackClient() *serialSendackClient {
	return &serialSendackClient{
		firstSend:    make(chan struct{}),
		secondSend:   make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
}

func (c *serialSendackClient) Connect(context.Context, string, string) error { return nil }
func (c *serialSendackClient) Close() error                                  { return nil }
func (c *serialSendackClient) RecvAck(context.Context, int64, uint64) error  { return nil }

func (c *serialSendackClient) Send(_ context.Context, pkt *frame.SendPacket) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cloned := *pkt
	c.sent = append(c.sent, &cloned)
	switch len(c.sent) {
	case 1:
		if !c.firstSendClosed {
			close(c.firstSend)
			c.firstSendClosed = true
		}
	case 2:
		if !c.secondSendClosed {
			close(c.secondSend)
			c.secondSendClosed = true
		}
	}
	return nil
}

func (c *serialSendackClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	var pkt *frame.SendPacket
	var idx int
	for {
		c.mu.Lock()
		if c.readIndex < len(c.sent) {
			idx = c.readIndex
			original := c.sent[idx]
			cloned := *original
			pkt = &cloned
			c.readIndex++
			c.mu.Unlock()
			break
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	if idx == 0 {
		select {
		case <-c.releaseFirst:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &frame.SendackPacket{
		ClientSeq:   pkt.ClientSeq,
		ClientMsgNo: pkt.ClientMsgNo,
		ReasonCode:  frame.ReasonSuccess,
	}, nil
}

func (c *serialSendackClient) waitFirstSend(t *testing.T, timeout time.Duration) bool {
	t.Helper()
	select {
	case <-c.firstSend:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (c *serialSendackClient) waitSecondSend(timeout time.Duration) bool {
	select {
	case <-c.secondSend:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (c *serialSendackClient) releaseFirstAck() {
	close(c.releaseFirst)
}

func (c *serialSendackClient) sentClientSeqs() []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	seqs := make([]uint64, 0, len(c.sent))
	for _, pkt := range c.sent {
		seqs = append(seqs, pkt.ClientSeq)
	}
	return seqs
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

func TestGroupWorkloadRunHonorsLocalRateDurationPerChannel(t *testing.T) {
	clients := map[string]PersonClient{
		"u-0": newRecordingPersonClient(),
		"u-1": newRecordingPersonClient(),
	}
	clients["u-0"].(*recordingPersonClient).autoSendack = true
	var sleeps []time.Duration
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "many-group",
		TrafficName:     "group-send",
		ClientMsgPrefix: "bench-msg",
		RunDuration:     time.Second,
		LocalRate:       model.Rate{PerSecond: 3},
		Channels: []GroupChannel{{
			ChannelIndex:  0,
			ChannelID:     "run-a-many-group-0",
			OnlineMembers: []string{"u-0", "u-1"},
		}},
		Metrics: metrics.NewRegistry(),
		sleep: func(ctx context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
	}, clients)
	require.NoError(t, err)

	require.NoError(t, workload.Run(context.Background()))

	sender := clients["u-0"].(*recordingPersonClient)
	require.Len(t, sender.sentFrames, 3)
	require.Len(t, sleeps, 3)
	for _, d := range sleeps {
		require.Equal(t, time.Second/3, d)
	}
	require.Equal(t, uint64(3), workload.Metrics().CounterValue("group_send_success_total", groupSendLabels("run", "many-group", "group-send")))
}

func TestGroupWorkloadRoundRobinSenderPickFansIntoOneChannel(t *testing.T) {
	clients := map[string]PersonClient{
		"u-0": newRecordingPersonClient(),
		"u-1": newRecordingPersonClient(),
		"u-2": newRecordingPersonClient(),
	}
	for _, client := range clients {
		client.(*recordingPersonClient).autoSendack = true
	}
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "hot-group",
		TrafficName:     "hot-send",
		ClientMsgPrefix: "bench-msg",
		RunDuration:     time.Second,
		LocalRate:       model.Rate{PerSecond: 3},
		MaxConcurrency:  3,
		SenderPick:      "round_robin",
		Channels: []GroupChannel{{
			ChannelIndex:  0,
			ChannelID:     "run-a-hot-group-0",
			OnlineMembers: []string{"u-0", "u-1", "u-2"},
		}},
		Metrics: metrics.NewRegistry(),
		sleep: func(ctx context.Context, d time.Duration) error {
			return nil
		},
	}, clients)
	require.NoError(t, err)

	require.NoError(t, workload.Run(context.Background()))

	for _, uid := range []string{"u-0", "u-1", "u-2"} {
		sender := clients[uid].(*recordingPersonClient)
		require.Len(t, sender.sentFrames, 1, "sender %s should send one message", uid)
		require.Equal(t, "run-a-hot-group-0", sender.sentFrames[0].ChannelID)
	}
	require.Equal(t, uint64(3), workload.Metrics().CounterValue("group_send_success_total", groupSendLabels("run", "hot-group", "hot-send")))
}

func TestGroupWorkloadRunRecordsSchedulerWindowDropMetrics(t *testing.T) {
	sender := newDelayedSendackClient(30 * time.Millisecond)
	clients := map[string]PersonClient{
		"u-0": sender,
		"u-1": newRecordingPersonClient(),
	}
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "hot-group",
		TrafficName:     "hot-send",
		ClientMsgPrefix: "bench-msg",
		RunDuration:     15 * time.Millisecond,
		LocalRate:       model.Rate{PerSecond: 200},
		MaxConcurrency:  3,
		SenderPick:      "first_online",
		Channels: []GroupChannel{{
			ChannelIndex:  0,
			ChannelID:     "run-a-hot-group-0",
			OnlineMembers: []string{"u-0", "u-1"},
		}},
		Metrics: metrics.NewRegistry(),
	}, clients)
	require.NoError(t, err)

	require.NoError(t, workload.Run(context.Background()))

	labels := groupSendLabels("run", "hot-group", "hot-send")
	require.Equal(t, uint64(3), workload.Metrics().CounterValue("workload_scheduler_planned_total", labels))
	require.Equal(t, uint64(3), workload.Metrics().CounterValue("workload_scheduler_enqueued_total", labels))
	require.Equal(t, uint64(1), workload.Metrics().CounterValue("workload_scheduler_dispatched_total", labels))
	require.Equal(t, uint64(2), workload.Metrics().CounterValue("workload_scheduler_dropped_total", schedulerDropLabels(labels, "pending_window_expired")))
	require.GreaterOrEqual(t, workload.Metrics().CounterValue("workload_scheduler_busy_key_stall_total", labels), uint64(1))
	require.Equal(t, float64(1), workload.Metrics().GaugeValue("workload_scheduler_max_active", labels))
	require.GreaterOrEqual(t, workload.Metrics().GaugeValue("workload_scheduler_max_pending", labels), float64(1))
}

func TestGroupWorkloadWarmupTouchesEveryChannelAtLeastOnce(t *testing.T) {
	clients := map[string]PersonClient{
		"u-0": newRecordingPersonClient(),
		"u-1": newRecordingPersonClient(),
	}
	clients["u-0"].(*recordingPersonClient).autoSendack = true
	workload, err := NewGroupWorkload(GroupConfig{
		RunID:           "run-a",
		ProfileName:     "many-group",
		TrafficName:     "group-send",
		ClientMsgPrefix: "bench-msg",
		WarmupDuration:  10 * time.Second,
		LocalRate:       model.Rate{PerSecond: 0.25},
		Channels: []GroupChannel{
			{ChannelIndex: 0, ChannelID: "run-a-many-group-0", OnlineMembers: []string{"u-0", "u-1"}},
			{ChannelIndex: 1, ChannelID: "run-a-many-group-1", OnlineMembers: []string{"u-0", "u-1"}},
			{ChannelIndex: 2, ChannelID: "run-a-many-group-2", OnlineMembers: []string{"u-0", "u-1"}},
		},
		Metrics: metrics.NewRegistry(),
		sleep: func(ctx context.Context, d time.Duration) error {
			return nil
		},
	}, clients)
	require.NoError(t, err)

	require.NoError(t, workload.Warmup(context.Background()))

	sender := clients["u-0"].(*recordingPersonClient)
	require.Len(t, sender.sentFrames, 3)
	require.Equal(t, []string{
		"run-a-many-group-0",
		"run-a-many-group-1",
		"run-a-many-group-2",
	}, []string{
		sender.sentFrames[0].ChannelID,
		sender.sentFrames[1].ChannelID,
		sender.sentFrames[2].ChannelID,
	})
	require.Equal(t, uint64(3), workload.Metrics().CounterValue("group_send_success_total", groupSendLabels("warmup", "many-group", "group-send")))
}

func groupSendLabels(phase, profile, traffic string) metrics.Labels {
	return metrics.Labels{"phase": phase, "channel_type": "group", "profile": profile, "traffic": traffic}
}
