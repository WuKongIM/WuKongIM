package message

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	channelmembers "github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/internal/observability/sendtrace"
	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

var fixedSendNow = time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

func TestSendRejectsUnauthenticatedSender(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.ErrorIs(t, err, ErrUnauthenticatedSender)
	require.Equal(t, SendResult{}, result)
}

func TestSendReturnsUnsupportedChannelType(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "slot-1",
		ChannelType: 99,
		ClientSeq:   12,
		ClientMsgNo: "m3",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonNotSupportChannelType, result.Reason)
	require.Zero(t, result.MessageID)
	require.Zero(t, result.MessageSeq)
}

func TestSendReturnsClusterRequiredWhenClusterNotConfigured(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.ErrorIs(t, err, ErrClusterRequired)
	require.Equal(t, SendResult{}, result)
}

func TestSendNoPersistReturnsSuccessWithoutCluster(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true},
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Zero(t, result.MessageID)
	require.Zero(t, result.MessageSeq)
}

func TestSendNoPersistSkipsDurableAppendAndCommittedDispatch(t *testing.T) {
	cluster := &fakeChannelCluster{}
	dispatcher := &recordingCommittedDispatcher{}
	app := New(Options{
		Now:                 fixedNowFn,
		Cluster:             cluster,
		CommittedDispatcher: dispatcher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true},
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Zero(t, result.MessageID)
	require.Zero(t, result.MessageSeq)
	require.Empty(t, cluster.sendRequests)
	require.Empty(t, dispatcher.calls)
}

func TestSendNoPersistStillChecksPermissions(t *testing.T) {
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("u1", int64(frame.ChannelTypePerson))] = metadb.Channel{
		ChannelID:   "u1",
		ChannelType: int64(frame.ChannelTypePerson),
		SendBan:     1,
	}
	app := New(Options{
		Now:             fixedNowFn,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true},
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSendBan, result.Reason)
}

func TestSendRequestScopedRejectsWithoutSyncOnce(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:            "system",
		RequestSubscribers: []string{"u1"},
		Payload:            []byte("cmd"),
	})

	require.ErrorIs(t, err, ErrRequestSubscribersRequireSyncOnce)
	require.Equal(t, SendResult{}, result)
}

func TestSendRequestScopedRejectsChannelIDConflict(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:             frame.Framer{SyncOnce: true},
		FromUID:            "system",
		ChannelID:          "g1",
		ChannelType:        frame.ChannelTypeGroup,
		RequestSubscribers: []string{"u1"},
		Payload:            []byte("cmd"),
	})

	require.ErrorIs(t, err, ErrRequestSubscribersConflictChannel)
	require.Equal(t, SendResult{}, result)
}

func TestSendRequestScopedRejectsEmptySubscribers(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:             frame.Framer{SyncOnce: true},
		FromUID:            "system",
		RequestSubscribers: []string{" ", ""},
		Payload:            []byte("cmd"),
	})

	require.ErrorIs(t, err, ErrRequestSubscribersRequired)
	require.Equal(t, SendResult{}, result)
}

func TestSendRequestScopedDurableAppendsTempCommandAndDispatchesSubscribers(t *testing.T) {
	dispatcher := &recordingCommittedDispatcher{}
	cluster := &fakeChannelCluster{
		sendFn: func(_ context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
			msg := req.Message
			msg.MessageID = 808
			msg.MessageSeq = 18
			return channel.AppendResult{MessageID: 808, MessageSeq: 18, Message: msg}, nil
		},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		Cluster:             cluster,
		MetaRefresher:       &fakeMetaRefresher{},
		CommittedDispatcher: dispatcher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:             frame.Framer{SyncOnce: true},
		FromUID:            "system",
		ChannelType:        frame.ChannelTypeGroup,
		RequestSubscribers: []string{"u1", "u2", "u1"},
		Payload:            []byte("cmd"),
		ClientMsgNo:        "request-scoped-1",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(808), result.MessageID)
	require.Equal(t, uint64(18), result.MessageSeq)

	scoped, err := runtimechannelid.RequestSubscriberChannelFor([]string{"u1", "u2"})
	require.NoError(t, err)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: scoped.CommandChannelID, Type: frame.ChannelTypeTemp}, cluster.sendRequests[0].ChannelID)
	require.Equal(t, scoped.CommandChannelID, cluster.sendRequests[0].Message.ChannelID)
	require.Equal(t, frame.ChannelTypeTemp, cluster.sendRequests[0].Message.ChannelType)
	require.Equal(t, frame.Framer{SyncOnce: true}, cluster.sendRequests[0].Message.Framer)
	require.Equal(t, "system", cluster.sendRequests[0].Message.FromUID)
	require.Equal(t, []byte("cmd"), cluster.sendRequests[0].Message.Payload)

	require.Len(t, dispatcher.calls, 1)
	require.Equal(t, []string{"u1", "u2"}, dispatcher.calls[0].MessageScopedUIDs)
	require.Equal(t, scoped.CommandChannelID, dispatcher.calls[0].Message.ChannelID)
	require.Equal(t, frame.ChannelTypeTemp, dispatcher.calls[0].Message.ChannelType)
}

func TestSendRejectsBannedGroupBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelCluster{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
		Ban:         1,
	}
	app := New(Options{
		Now:             fixedNowFn,
		Cluster:         cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonBan, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendRejectsSenderSendBanBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelCluster{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("u1", int64(frame.ChannelTypePerson))] = metadb.Channel{
		ChannelID:   "u1",
		ChannelType: int64(frame.ChannelTypePerson),
		SendBan:     1,
	}
	app := New(Options{
		Now:             fixedNowFn,
		Cluster:         cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSendBan, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendRejectsGroupDisbandBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelCluster{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
		Disband:     1,
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:             fixedNowFn,
		Cluster:         cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonDisband, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendRejectsGroupSenderInDenylistBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelCluster{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	denyID := channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup})
	permissions.members[permissionKey(denyID, int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:             fixedNowFn,
		Cluster:         cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonInBlacklist, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendRejectsGroupSenderThatIsNotSubscriberBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelCluster{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	app := New(Options{
		Now:             fixedNowFn,
		Cluster:         cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSubscriberNotExist, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendRejectsGroupSenderNotInNonEmptyAllowlistBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelCluster{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	allowID := channelmembers.AllowlistChannelID(channelmembers.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup})
	permissions.hasAny[permissionKey(allowID, int64(frame.ChannelTypeGroup))] = true
	permissions.members[permissionKey(allowID, int64(frame.ChannelTypeGroup))] = map[string]bool{"u2": true}
	app := New(Options{
		Now:             fixedNowFn,
		Cluster:         cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonNotInWhitelist, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendAllowsGroupSenderWhenSubscriberAndAllowlistEmpty(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 701, MessageSeq: 31}},
		},
	}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:             fixedNowFn,
		Cluster:         cluster,
		MetaRefresher:   &fakeMetaRefresher{},
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(701), result.MessageID)
	require.Equal(t, uint64(31), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendRejectsPersonSenderInReceiverDenylistBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelCluster{}
	permissions := newFakePermissionStore()
	denyID := channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: "u2", ChannelType: frame.ChannelTypePerson})
	permissions.members[permissionKey(denyID, int64(frame.ChannelTypePerson))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:             fixedNowFn,
		Cluster:         cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonInBlacklist, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendAllowsPersonSenderWhenReceiverDenylistMisses(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 702, MessageSeq: 32}},
		},
	}
	permissions := newFakePermissionStore()
	app := New(Options{
		Now:             fixedNowFn,
		Cluster:         cluster,
		MetaRefresher:   &fakeMetaRefresher{},
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(702), result.MessageID)
	require.Equal(t, uint64(32), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendSyncOncePlainDurablePersonStillCanonicalizesAndAppendsOnce(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 704, MessageSeq: 34}},
		},
	}
	app := New(Options{
		Now:           fixedNowFn,
		Cluster:       cluster,
		MetaRefresher: &fakeMetaRefresher{},
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(704), result.MessageID)
	require.Equal(t, uint64(34), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson}, cluster.sendRequests[0].ChannelID)
	require.Equal(t, "u2@u1", cluster.sendRequests[0].Message.ChannelID)
}

func TestSendSyncOnceNormalGroupAppendsToCommandChannelAfterPermissionChecks(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 705, MessageSeq: 35}},
		},
	}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:             fixedNowFn,
		Cluster:         cluster,
		MetaRefresher:   &fakeMetaRefresher{},
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{SyncOnce: true},
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(705), result.MessageID)
	require.Equal(t, uint64(35), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: runtimechannelid.ToCommandChannel("g1"), Type: frame.ChannelTypeGroup}, cluster.sendRequests[0].ChannelID)
	require.Equal(t, runtimechannelid.ToCommandChannel("g1"), cluster.sendRequests[0].Message.ChannelID)
}

func TestSendAlreadyDerivedGroupChecksSourceAndAppendsWithoutDoubleCommandSuffix(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 706, MessageSeq: 36}},
		},
	}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:             fixedNowFn,
		Cluster:         cluster,
		MetaRefresher:   &fakeMetaRefresher{},
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   runtimechannelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(706), result.MessageID)
	require.Equal(t, uint64(36), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: runtimechannelid.ToCommandChannel("g1"), Type: frame.ChannelTypeGroup}, cluster.sendRequests[0].ChannelID)
	require.NotContains(t, cluster.sendRequests[0].ChannelID.ID, runtimechannelid.CommandChannelSuffix+runtimechannelid.CommandChannelSuffix)
}

func TestSendAlreadyDerivedPersonStripsBeforeNormalizeAndRestoresCommandSuffixOnce(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 707, MessageSeq: 37}},
		},
	}
	app := New(Options{
		Now:           fixedNowFn,
		Cluster:       cluster,
		MetaRefresher: &fakeMetaRefresher{},
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   runtimechannelid.ToCommandChannel("u1@u2"),
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(707), result.MessageID)
	require.Equal(t, uint64(37), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: runtimechannelid.ToCommandChannel("u2@u1"), Type: frame.ChannelTypePerson}, cluster.sendRequests[0].ChannelID)
	require.Equal(t, runtimechannelid.ToCommandChannel("u2@u1"), cluster.sendRequests[0].Message.ChannelID)
	require.NotContains(t, cluster.sendRequests[0].ChannelID.ID, runtimechannelid.CommandChannelSuffix+runtimechannelid.CommandChannelSuffix)
}

func TestSendNoPersistWithSyncOnceReturnsSuccessWithoutDurableAppend(t *testing.T) {
	cluster := &fakeChannelCluster{}
	dispatcher := &recordingCommittedDispatcher{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:                 fixedNowFn,
		Cluster:             cluster,
		CommittedDispatcher: dispatcher,
		PermissionStore:     permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true, SyncOnce: true},
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Zero(t, result.MessageID)
	require.Zero(t, result.MessageSeq)
	require.Empty(t, cluster.sendRequests)
	require.Empty(t, dispatcher.calls)
}

func TestSendSystemUIDBypassesPermissionChecks(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 703, MessageSeq: 33}},
		},
	}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("sys", int64(frame.ChannelTypePerson))] = metadb.Channel{
		ChannelID:   "sys",
		ChannelType: int64(frame.ChannelTypePerson),
		SendBan:     1,
	}
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
		Disband:     1,
	}
	denyID := channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup})
	permissions.members[permissionKey(denyID, int64(frame.ChannelTypeGroup))] = map[string]bool{"sys": true}
	app := New(Options{
		Now:             fixedNowFn,
		Cluster:         cluster,
		MetaRefresher:   &fakeMetaRefresher{},
		PermissionStore: permissions,
		SystemUIDs:      fakeSystemUIDChecker{"sys": true},
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "sys",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(703), result.MessageID)
	require.Equal(t, uint64(33), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendPassesTraceIDToChannelAppend(t *testing.T) {
	cluster := &fakeChannelCluster{}
	app := New(Options{
		Now:           fixedNowFn,
		Cluster:       cluster,
		MetaRefresher: &fakeMetaRefresher{},
	})

	_, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
		ClientMsgNo: "m-trace",
		TraceID:     "trace-1",
	})

	require.NoError(t, err)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, "trace-1", cluster.sendRequests[0].TraceID)
	require.Equal(t, 0, cluster.sendRequests[0].Attempt)
}

func TestSendRecordsDurableTraceWithChannelKey(t *testing.T) {
	sink := &recordingMessageSendTraceSink{}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)
	cluster := &fakeChannelCluster{sendReplies: []fakeChannelClusterSendReply{{result: channel.AppendResult{MessageID: 99, MessageSeq: 9}}}}
	app := New(Options{
		Now:           fixedNowFn,
		Cluster:       cluster,
		MetaRefresher: &fakeMetaRefresher{},
	})

	_, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
		ClientMsgNo: "m-channel",
		TraceID:     "trace-1",
	})

	require.NoError(t, err)
	var durable sendtrace.Event
	for _, event := range sink.events {
		if event.Stage == sendtrace.StageMessageSendDurable {
			durable = event
			break
		}
	}
	require.Equal(t, "trace-1", durable.TraceID)
	require.Equal(t, "m-channel", durable.ClientMsgNo)
	require.Equal(t, uint64(9), durable.MessageSeq)
	require.Equal(t, string(channelhandler.KeyFromChannelID(channel.ChannelID{ID: "g1", Type: frame.ChannelTypeGroup})), durable.ChannelKey)
}

func TestSendReturnsSuccessAfterDurableWriteAndSubmitsCommittedMessage(t *testing.T) {
	dispatcher := &recordingCommittedDispatcher{}
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{
				MessageID:  99,
				MessageSeq: 7,
				Message: channel.Message{
					MessageID:   99,
					MessageSeq:  7,
					Framer:      frame.Framer{RedDot: true, SyncOnce: true},
					Setting:     frame.SettingReceiptEnabled,
					MsgKey:      "k1",
					Expire:      60,
					ClientSeq:   9,
					ClientMsgNo: "m1",
					StreamNo:    "stream-1",
					Timestamp:   int32(fixedSendNow.Unix()),
					ChannelID:   runtimechannelid.ToCommandChannel("u2@u1"),
					ChannelType: frame.ChannelTypePerson,
					Topic:       "chat",
					FromUID:     "u1",
					Payload:     []byte("hi"),
				},
			}},
		},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		Cluster:             cluster,
		MetaRefresher:       &fakeMetaRefresher{},
		CommittedDispatcher: dispatcher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:          frame.Framer{RedDot: true, SyncOnce: true},
		Setting:         frame.SettingReceiptEnabled,
		MsgKey:          "k1",
		Expire:          60,
		FromUID:         "u1",
		SenderSessionID: 42,
		ChannelID:       "u2",
		ChannelType:     frame.ChannelTypePerson,
		Topic:           "chat",
		Payload:         []byte("hi"),
		ClientSeq:       9,
		ClientMsgNo:     "m1",
		StreamNo:        "stream-1",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(99), result.MessageID)
	require.Equal(t, uint64(7), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: runtimechannelid.ToCommandChannel("u2@u1"), Type: frame.ChannelTypePerson}, cluster.sendRequests[0].ChannelID)
	require.Equal(t, frame.Framer{RedDot: true, SyncOnce: true}, cluster.sendRequests[0].Message.Framer)
	require.Equal(t, frame.SettingReceiptEnabled, cluster.sendRequests[0].Message.Setting)
	require.Equal(t, "k1", cluster.sendRequests[0].Message.MsgKey)
	require.Equal(t, uint32(60), cluster.sendRequests[0].Message.Expire)
	require.Equal(t, uint64(9), cluster.sendRequests[0].Message.ClientSeq)
	require.Equal(t, "m1", cluster.sendRequests[0].Message.ClientMsgNo)
	require.Equal(t, "stream-1", cluster.sendRequests[0].Message.StreamNo)
	require.Equal(t, int32(fixedSendNow.Unix()), cluster.sendRequests[0].Message.Timestamp)
	require.Equal(t, runtimechannelid.ToCommandChannel("u2@u1"), cluster.sendRequests[0].Message.ChannelID)
	require.Equal(t, "chat", cluster.sendRequests[0].Message.Topic)
	require.Equal(t, "u1", cluster.sendRequests[0].Message.FromUID)
	require.Equal(t, []byte("hi"), cluster.sendRequests[0].Message.Payload)
	require.Equal(t, channel.CommitModeQuorum, cluster.sendRequests[0].CommitMode)
	require.Len(t, dispatcher.calls, 1)
	require.Equal(t, uint64(42), dispatcher.calls[0].SenderSessionID)
	require.Equal(t, channel.Message{
		MessageID:   99,
		MessageSeq:  7,
		Framer:      frame.Framer{RedDot: true, SyncOnce: true},
		Setting:     frame.SettingReceiptEnabled,
		MsgKey:      "k1",
		Expire:      60,
		ClientSeq:   9,
		ClientMsgNo: "m1",
		StreamNo:    "stream-1",
		Timestamp:   int32(fixedSendNow.Unix()),
		ChannelID:   runtimechannelid.ToCommandChannel("u2@u1"),
		ChannelType: frame.ChannelTypePerson,
		Topic:       "chat",
		FromUID:     "u1",
		Payload:     []byte("hi"),
	}, dispatcher.calls[0].Message)
}

func TestSendDefaultsToQuorumCommitMode(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 88, MessageSeq: 12}},
		},
	}
	app := New(Options{
		Now:           fixedNowFn,
		Cluster:       cluster,
		MetaRefresher: &fakeMetaRefresher{},
	})

	_, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})
	require.NoError(t, err)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.CommitModeQuorum, cluster.sendRequests[0].CommitMode)
}

func TestSendPropagatesLocalCommitMode(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 89, MessageSeq: 13}},
		},
	}
	app := New(Options{
		Now:           fixedNowFn,
		Cluster:       cluster,
		MetaRefresher: &fakeMetaRefresher{},
	})

	_, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		CommitMode:  channel.CommitModeLocal,
		Payload:     []byte("hi"),
	})
	require.NoError(t, err)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.CommitModeLocal, cluster.sendRequests[0].CommitMode)
}

func TestSendRecanonicalizesPrecomposedPersonChannelBeforeDurableWrite(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 88, MessageSeq: 12}},
		},
	}
	app := New(Options{
		Now:           fixedNowFn,
		Cluster:       cluster,
		MetaRefresher: &fakeMetaRefresher{},
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u1@u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson}, cluster.sendRequests[0].ChannelID)
}

func TestSendRejectsThirdPartyPrecomposedPersonChannel(t *testing.T) {
	cluster := &fakeChannelCluster{}
	app := New(Options{
		Now:     fixedNowFn,
		Cluster: cluster,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u3@u4",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.Error(t, err)
	require.Equal(t, SendResult{}, result)
	require.Empty(t, cluster.sendRequests)
}

func TestSendReturnsSuccessWhenCommittedSubmitFails(t *testing.T) {
	dispatcher := &recordingCommittedDispatcher{err: errors.New("queue full")}
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 101, MessageSeq: 5}},
		},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		Cluster:             cluster,
		MetaRefresher:       &fakeMetaRefresher{},
		CommittedDispatcher: dispatcher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
		ClientSeq:   14,
		ClientMsgNo: "m5",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(101), result.MessageID)
	require.Equal(t, uint64(5), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Len(t, dispatcher.calls, 1)
}

func TestSendSubmitsCommittedMessageFromClusterResult(t *testing.T) {
	dispatcher := &recordingCommittedDispatcher{}
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{
				MessageID:  88,
				MessageSeq: 7,
				Message: channel.Message{
					MessageID:   88,
					MessageSeq:  7,
					ChannelID:   "u2@u1",
					ChannelType: frame.ChannelTypePerson,
					FromUID:     "committed-sender",
					ClientMsgNo: "committed-1",
					Topic:       "committed-topic",
					Payload:     []byte("committed-payload"),
				},
			}},
		},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		Cluster:             cluster,
		MetaRefresher:       &fakeMetaRefresher{},
		CommittedDispatcher: dispatcher,
	})

	_, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "draft-1",
		Topic:       "draft-topic",
		Payload:     []byte("draft-payload"),
	})

	require.NoError(t, err)
	require.Len(t, dispatcher.calls, 1)
	require.Equal(t, "committed-sender", dispatcher.calls[0].Message.FromUID)
	require.Equal(t, "committed-topic", dispatcher.calls[0].Message.Topic)
	require.Equal(t, []byte("committed-payload"), dispatcher.calls[0].Message.Payload)
}

func TestSendDoesNotPerformSynchronousDeliveryAfterDurableWrite(t *testing.T) {
	reg := &fakeRegistry{
		byUID: map[string][]online.OnlineConn{
			"u2": {
				{SessionID: 2, UID: "u2"},
				{SessionID: 99, UID: "u2"},
			},
		},
	}
	delivery := &recordingDelivery{}
	remote := &recordingRemoteDelivery{}
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 601, MessageSeq: 22}},
		},
	}
	recipients := fakeRecipientDirectory{
		endpointsByUID: map[string][]Endpoint{
			"u2": {
				{NodeID: 1, BootID: 11, SessionID: 2},
				{NodeID: 2, BootID: 22, SessionID: 8},
			},
		},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		Cluster:             cluster,
		MetaRefresher:       &fakeMetaRefresher{},
		Online:              reg,
		Delivery:            delivery,
		Recipients:          recipients,
		RemoteDelivery:      remote,
		LocalNodeID:         1,
		LocalBootID:         11,
		CommittedDispatcher: &recordingCommittedDispatcher{},
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
		ClientSeq:   31,
		ClientMsgNo: "m-remote",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Empty(t, delivery.calls)
	require.Empty(t, remote.calls)
}

func TestSendRefreshesMetaThenAppendsLocally(t *testing.T) {
	dispatcher := &recordingCommittedDispatcher{}
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{
				MessageID:  201,
				MessageSeq: 7,
				Message: channel.Message{
					MessageID:   201,
					MessageSeq:  7,
					ChannelID:   "u2@u1",
					ChannelType: frame.ChannelTypePerson,
					FromUID:     "u1",
					ClientMsgNo: "m6",
					Payload:     []byte("hi"),
					ClientSeq:   21,
				},
			}},
		},
	}
	refresher := &fakeMetaRefresher{
		metas: []channel.Meta{{
			ID:          channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
			Epoch:       11,
			LeaderEpoch: 3,
		}},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		Cluster:             cluster,
		MetaRefresher:       refresher,
		CommittedDispatcher: dispatcher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
		ClientSeq:   21,
		ClientMsgNo: "m6",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(201), result.MessageID)
	require.Equal(t, uint64(7), result.MessageSeq)
	require.Len(t, refresher.keys, 1)
	require.Equal(t, channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson}, refresher.keys[0])
	require.Empty(t, cluster.appliedMetas)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, uint64(11), cluster.sendRequests[0].ExpectedChannelEpoch)
	require.Equal(t, uint64(3), cluster.sendRequests[0].ExpectedLeaderEpoch)
	require.Equal(t, []deliveryEnvelopeRecord{{
		Message: channel.Message{
			MessageID:   201,
			MessageSeq:  7,
			ChannelID:   "u2@u1",
			ChannelType: frame.ChannelTypePerson,
			FromUID:     "u1",
			ClientMsgNo: "m6",
			Payload:     []byte("hi"),
			ClientSeq:   21,
		},
	}}, dispatcher.calls)
}

func TestSendBootstrapsAndAppendsLocally(t *testing.T) {
	dispatcher := &recordingCommittedDispatcher{}
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{
				MessageID:  301,
				MessageSeq: 4,
				Message: channel.Message{
					MessageID:   301,
					MessageSeq:  4,
					ChannelID:   "group-1",
					ChannelType: frame.ChannelTypeGroup,
					FromUID:     "u1",
					ClientMsgNo: "bootstrap-1",
					Payload:     []byte("hi group"),
					ClientSeq:   33,
				},
			}},
		},
	}
	refresher := &fakeMetaRefresher{
		metas: []channel.Meta{{
			ID:          channel.ChannelID{ID: "group-1", Type: frame.ChannelTypeGroup},
			Epoch:       17,
			LeaderEpoch: 6,
		}},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		Cluster:             cluster,
		MetaRefresher:       refresher,
		CommittedDispatcher: dispatcher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "group-1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi group"),
		ClientSeq:   33,
		ClientMsgNo: "bootstrap-1",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(301), result.MessageID)
	require.Equal(t, uint64(4), result.MessageSeq)
	require.Equal(t, []channel.ChannelID{{ID: "group-1", Type: frame.ChannelTypeGroup}}, refresher.keys)
	require.Empty(t, cluster.appliedMetas)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: "group-1", Type: frame.ChannelTypeGroup}, cluster.sendRequests[0].ChannelID)
	require.Equal(t, uint64(17), cluster.sendRequests[0].ExpectedChannelEpoch)
	require.Equal(t, uint64(6), cluster.sendRequests[0].ExpectedLeaderEpoch)
	require.Equal(t, []deliveryEnvelopeRecord{{
		Message: channel.Message{
			MessageID:   301,
			MessageSeq:  4,
			ChannelID:   "group-1",
			ChannelType: frame.ChannelTypeGroup,
			FromUID:     "u1",
			ClientMsgNo: "bootstrap-1",
			Payload:     []byte("hi group"),
			ClientSeq:   33,
		},
	}}, dispatcher.calls)
}

func TestSendRefreshDoesNotReapplyMeta(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 302, MessageSeq: 8}},
		},
		applyErr: channel.ErrStaleMeta,
	}
	refresher := &fakeMetaRefresher{
		metas: []channel.Meta{{
			ID:          channel.ChannelID{ID: "group-apply-once", Type: frame.ChannelTypeGroup},
			Epoch:       18,
			LeaderEpoch: 7,
		}},
	}
	app := New(Options{
		Now:           fixedNowFn,
		Cluster:       cluster,
		MetaRefresher: refresher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "group-apply-once",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi group"),
		ClientSeq:   35,
		ClientMsgNo: "apply-once-1",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(302), result.MessageID)
	require.Equal(t, uint64(8), result.MessageSeq)
	require.Equal(t, []channel.ChannelID{{ID: "group-apply-once", Type: frame.ChannelTypeGroup}}, refresher.keys)
	require.Empty(t, cluster.appliedMetas)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, uint64(18), cluster.sendRequests[0].ExpectedChannelEpoch)
	require.Equal(t, uint64(7), cluster.sendRequests[0].ExpectedLeaderEpoch)
}

func TestSendFailsWhenRefreshReturnsTopologyError(t *testing.T) {
	cluster := &fakeChannelCluster{}
	refresher := &fakeMetaRefresher{
		errs: []error{raftcluster.ErrNoLeader},
	}
	app := New(Options{
		Now:           fixedNowFn,
		Cluster:       cluster,
		MetaRefresher: refresher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "group-2",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi group"),
		ClientSeq:   34,
		ClientMsgNo: "bootstrap-err-1",
	})

	require.ErrorIs(t, err, raftcluster.ErrNoLeader)
	require.Equal(t, SendResult{}, result)
	require.Equal(t, []channel.ChannelID{{ID: "group-2", Type: frame.ChannelTypeGroup}}, refresher.keys)
	require.Empty(t, cluster.sendRequests)
	require.Empty(t, cluster.appliedMetas)
}

func TestSendDurablePersonPropagatesRequestContextToClusterAndMetaRefresh(t *testing.T) {
	type ctxKey string

	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 401, MessageSeq: 19}},
		},
	}
	refresher := &fakeMetaRefresher{
		metas: []channel.Meta{{
			ID:          channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
			Epoch:       12,
			LeaderEpoch: 4,
		}},
	}
	app := New(Options{
		Now:           fixedNowFn,
		Cluster:       cluster,
		MetaRefresher: refresher,
	})

	ctx := context.WithValue(context.Background(), ctxKey("request"), "durable-send")
	result, err := app.Send(ctx, SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "m9",
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Len(t, cluster.sendContexts, 1)
	require.Same(t, ctx, cluster.sendContexts[0])
	require.Len(t, refresher.refreshContexts, 1)
	require.Same(t, ctx, refresher.refreshContexts[0])
}

func TestSendDurablePersonReturnsContextCanceled(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendFn: func(ctx context.Context, _ channel.AppendRequest) (channel.AppendResult, error) {
			<-ctx.Done()
			return channel.AppendResult{}, ctx.Err()
		},
	}
	app := New(Options{
		Now:           fixedNowFn,
		Cluster:       cluster,
		MetaRefresher: &fakeMetaRefresher{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := app.Send(ctx, SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "m10",
		Payload:     []byte("hi"),
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, SendResult{}, result)
	require.Len(t, cluster.sendContexts, 1)
	require.Same(t, ctx, cluster.sendContexts[0])
}

func TestSendReturnsProtocolUpgradeRequiredWhenClusterRejectsLegacyClient(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{err: channel.ErrProtocolUpgradeRequired},
		},
	}
	delivery := &recordingDelivery{}
	app := New(Options{
		Now:           fixedNowFn,
		Cluster:       cluster,
		MetaRefresher: &fakeMetaRefresher{},
		Online: &fakeRegistry{
			byUID: map[string][]online.OnlineConn{
				"u2": {
					{SessionID: 2, UID: "u2"},
				},
			},
		},
		Delivery: delivery,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:         "u1",
		ChannelID:       "u2",
		ChannelType:     frame.ChannelTypePerson,
		Payload:         []byte("hi"),
		ClientSeq:       23,
		ClientMsgNo:     "m8",
		ProtocolVersion: frame.LegacyMessageSeqVersion,
	})

	require.ErrorIs(t, err, channel.ErrProtocolUpgradeRequired)
	require.Equal(t, SendResult{}, result)
	require.Empty(t, delivery.calls)
}

func TestNewPreservesInjectedCollaborators(t *testing.T) {
	identities := &fakeIdentityStore{}
	channels := &fakeChannelStore{}
	cluster := &fakeChannelCluster{}
	refresher := &fakeMetaRefresher{}
	remoteApp := &fakeRemoteAppender{}
	reg := &fakeRegistry{}
	delivery := &recordingDelivery{}
	dispatcher := &recordingCommittedDispatcher{}
	acks := &recordingDeliveryAck{}
	offline := &recordingDeliveryOffline{}
	permissions := newFakePermissionStore()
	systemUIDs := fakeSystemUIDChecker{"system": true}

	app := New(Options{
		IdentityStore:       identities,
		ChannelStore:        channels,
		PermissionStore:     permissions,
		SystemUIDs:          systemUIDs,
		Cluster:             cluster,
		MetaRefresher:       refresher,
		RemoteAppender:      remoteApp,
		Online:              reg,
		Delivery:            delivery,
		CommittedDispatcher: dispatcher,
		DeliveryAck:         acks,
		DeliveryOffline:     offline,
		LocalBootID:         9,
		Now:                 fixedNowFn,
	})

	require.Same(t, identities, app.identities)
	require.Same(t, channels, app.channels)
	require.Same(t, permissions, app.permissions)
	require.Equal(t, systemUIDs, app.systemUIDs)
	require.Same(t, cluster, app.cluster)
	require.Same(t, refresher, app.refresher)
	require.Same(t, remoteApp, app.remoteAppender)
	require.Same(t, reg, app.online)
	require.Same(t, delivery, app.delivery)
	require.Same(t, dispatcher, app.dispatcher)
	require.Same(t, acks, app.deliveryAck)
	require.Same(t, offline, app.deliveryOffline)
	require.Equal(t, uint64(9), app.localBootID)
	require.Same(t, reg, app.OnlineRegistry())
}

func fixedNowFn() time.Time {
	return fixedSendNow
}

type fakeRegistry struct {
	byUID map[string][]online.OnlineConn
}

func (f *fakeRegistry) Register(conn online.OnlineConn) error {
	if f.byUID == nil {
		f.byUID = make(map[string][]online.OnlineConn)
	}
	f.byUID[conn.UID] = append(f.byUID[conn.UID], conn)
	return nil
}

func (f *fakeRegistry) Unregister(sessionID uint64) {
	for uid, conns := range f.byUID {
		filtered := conns[:0]
		for _, conn := range conns {
			if conn.SessionID != sessionID {
				filtered = append(filtered, conn)
			}
		}
		if len(filtered) == 0 {
			delete(f.byUID, uid)
			continue
		}
		f.byUID[uid] = filtered
	}
}

func (f *fakeRegistry) MarkClosing(uint64) (online.OnlineConn, bool) {
	return online.OnlineConn{}, false
}

func (f *fakeRegistry) Connection(sessionID uint64) (online.OnlineConn, bool) {
	for _, conns := range f.byUID {
		for _, conn := range conns {
			if conn.SessionID == sessionID {
				return conn, true
			}
		}
	}
	return online.OnlineConn{}, false
}

func (f *fakeRegistry) ConnectionsByUID(uid string) []online.OnlineConn {
	conns := f.byUID[uid]
	out := make([]online.OnlineConn, len(conns))
	copy(out, conns)
	return out
}

func (f *fakeRegistry) ActiveConnectionsBySlot(uint64) []online.OnlineConn {
	return nil
}

func (f *fakeRegistry) ActiveSlots() []online.SlotSnapshot {
	return nil
}

func (f *fakeRegistry) Summary() online.Summary {
	summary := online.Summary{SessionsByListener: make(map[string]int)}
	for _, conns := range f.byUID {
		for _, conn := range conns {
			summary.Total++
			if conn.State == online.LocalRouteStateClosing {
				summary.Closing++
			} else {
				summary.Active++
			}
			summary.SessionsByListener[conn.Listener]++
		}
	}
	return summary
}

type deliveryCall struct {
	recipients []online.OnlineConn
	frame      frame.Frame
}

type recordingDelivery struct {
	calls []deliveryCall
}

func (d *recordingDelivery) Deliver(recipients []online.OnlineConn, f frame.Frame) error {
	copiedRecipients := make([]online.OnlineConn, len(recipients))
	copy(copiedRecipients, recipients)
	d.calls = append(d.calls, deliveryCall{recipients: copiedRecipients, frame: f})
	return nil
}

type fakeRecipientDirectory struct {
	endpointsByUID map[string][]Endpoint
	err            error
}

func (f fakeRecipientDirectory) EndpointsByUID(_ context.Context, uid string) ([]Endpoint, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]Endpoint(nil), f.endpointsByUID[uid]...), nil
}

type recordingRemoteDelivery struct {
	calls []RemoteDeliveryCommand
}

func (d *recordingRemoteDelivery) DeliverRemote(_ context.Context, cmd RemoteDeliveryCommand) error {
	copied := cmd
	copied.SessionIDs = append([]uint64(nil), cmd.SessionIDs...)
	d.calls = append(d.calls, copied)
	return nil
}

type recordingCommittedDispatcher struct {
	calls []deliveryEnvelopeRecord
	err   error
}

func (d *recordingCommittedDispatcher) SubmitCommitted(_ context.Context, env messageevents.MessageCommitted) error {
	copied := env
	copied.Message.Payload = append([]byte(nil), env.Message.Payload...)
	d.calls = append(d.calls, deliveryEnvelopeRecord(copied))
	return d.err
}

type deliveryEnvelopeRecord = messageevents.MessageCommitted

type fakeIdentityStore struct{}

func (*fakeIdentityStore) GetUser(context.Context, string) (metadb.User, error) {
	return metadb.User{}, nil
}

type fakeChannelStore struct{}

func (*fakeChannelStore) GetChannel(context.Context, string, int64) (metadb.Channel, error) {
	return metadb.Channel{}, nil
}

type fakePermissionStore struct {
	channels map[string]metadb.Channel
	members  map[string]map[string]bool
	hasAny   map[string]bool
}

func newFakePermissionStore() *fakePermissionStore {
	return &fakePermissionStore{
		channels: make(map[string]metadb.Channel),
		members:  make(map[string]map[string]bool),
		hasAny:   make(map[string]bool),
	}
}

func permissionKey(channelID string, channelType int64) string {
	return channelID + "#" + strconv.FormatInt(channelType, 10)
}

func (s *fakePermissionStore) GetChannelForPermission(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	ch, ok := s.channels[permissionKey(channelID, channelType)]
	if !ok {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return ch, nil
}

func (s *fakePermissionStore) ContainsChannelSubscriber(_ context.Context, channelID string, channelType int64, uid string) (bool, error) {
	return s.members[permissionKey(channelID, channelType)][uid], nil
}

func (s *fakePermissionStore) HasChannelSubscribers(_ context.Context, channelID string, channelType int64) (bool, error) {
	return s.hasAny[permissionKey(channelID, channelType)], nil
}

type fakeSystemUIDChecker map[string]bool

func (f fakeSystemUIDChecker) IsSystemUID(uid string) bool { return f[uid] }

type fakeChannelClusterSendReply struct {
	result channel.AppendResult
	err    error
}

type fakeChannelCluster struct {
	appliedMetas []channel.Meta
	sendRequests []channel.AppendRequest
	sendContexts []context.Context
	sendReplies  []fakeChannelClusterSendReply
	sendFn       func(context.Context, channel.AppendRequest) (channel.AppendResult, error)
	applyErr     error
}

func (f *fakeChannelCluster) ApplyMeta(meta channel.Meta) error {
	f.appliedMetas = append(f.appliedMetas, meta)
	return f.applyErr
}

func (f *fakeChannelCluster) Append(ctx context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
	f.sendContexts = append(f.sendContexts, ctx)
	f.sendRequests = append(f.sendRequests, req)
	if f.sendFn != nil {
		return f.sendFn(ctx, req)
	}
	if len(f.sendReplies) == 0 {
		return channel.AppendResult{}, nil
	}
	reply := f.sendReplies[0]
	f.sendReplies = f.sendReplies[1:]
	return reply.result, reply.err
}

type fakeMetaRefresher struct {
	keys            []channel.ChannelID
	refreshContexts []context.Context
	metas           []channel.Meta
	errs            []error
}

func (f *fakeMetaRefresher) RefreshChannelMeta(ctx context.Context, key channel.ChannelID) (channel.Meta, error) {
	f.keys = append(f.keys, key)
	f.refreshContexts = append(f.refreshContexts, ctx)
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return channel.Meta{}, err
		}
	}
	if len(f.metas) == 0 {
		return channel.Meta{}, nil
	}
	meta := f.metas[0]
	f.metas = f.metas[1:]
	return meta, nil
}

// recordingMessageSendTraceSink captures synchronous sendtrace events emitted by message tests.
type recordingMessageSendTraceSink struct {
	events []sendtrace.Event
}

func (s *recordingMessageSendTraceSink) RecordSendTrace(event sendtrace.Event) {
	s.events = append(s.events, event)
}
