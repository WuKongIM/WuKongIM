package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"unsafe"

	"github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestCMDSyncMessageStoreLocalReadsCommandMessagesFromSeq(t *testing.T) {
	id := channel.ChannelID{ID: channelid.ToCommandChannel("g-local"), Type: frame.ChannelTypeGroup}
	engine := openManagerMessageTestEngine(t)
	appendManagerMessageTestRows(t, engine, id, 4)

	store := cmdsyncMessageStore{
		localNodeID: 1,
		channelLog:  engine,
		metas: cmdsyncMessageMetasFake{meta: metadb.ChannelRuntimeMeta{
			ChannelID:   id.ID,
			ChannelType: int64(id.Type),
			Leader:      1,
		}},
	}

	messages, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: id.ID, ChannelType: id.Type}, 3, 10)
	require.NoError(t, err)
	require.Equal(t, []uint64{3, 4}, cmdsyncMessageSeqs(messages))
	require.Equal(t, id.ID, messages[0].ChannelID)
}

func TestCMDSyncMessageStoreRemoteReadsCommandOwnerWithSyncMode(t *testing.T) {
	id := channel.ChannelID{ID: channelid.ToCommandChannel("g-remote"), Type: frame.ChannelTypeGroup}
	remote := &cmdsyncMessageRemoteCapture{page: node.ChannelMessagesPage{Messages: []channel.Message{{MessageSeq: 7, ChannelID: id.ID, ChannelType: id.Type}}}}
	store := cmdsyncMessageStore{
		localNodeID: 1,
		metas: cmdsyncMessageMetasFake{meta: metadb.ChannelRuntimeMeta{
			ChannelID:           id.ID,
			ChannelType:         int64(id.Type),
			Leader:              9,
			RetentionThroughSeq: 4,
		}},
		remote: remote,
	}

	messages, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: id.ID, ChannelType: id.Type}, 5, 20)
	require.NoError(t, err)
	require.Equal(t, []uint64{7}, cmdsyncMessageSeqs(messages))
	require.Equal(t, []uint64{9}, remote.nodeIDs)
	require.Len(t, remote.requests, 1)
	require.Equal(t, id, remote.requests[0].ChannelID)
	require.True(t, remote.requests[0].SyncMode)
	require.Equal(t, uint64(5), remote.requests[0].StartSeq)
	require.Equal(t, 20, remote.requests[0].Limit)
	require.Equal(t, uint8(channelhandler.SyncPullModeUp), remote.requests[0].PullMode)
	require.Equal(t, uint64(5), remote.requests[0].MinAvailableSeq)
}

func TestCMDSyncMessageStoreReturnsEmptyWhenCommandLogUnavailable(t *testing.T) {
	id := channel.ChannelID{ID: channelid.ToCommandChannel("g-missing"), Type: frame.ChannelTypeGroup}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "not ready", err: channel.ErrNotReady},
		{name: "not found", err: channel.ErrChannelNotFound},
		{name: "remote not ready text", err: errors.New("rpc failed: channel: not ready")},
		{name: "remote not found text", err: errors.New("rpc failed: channel: channel not found")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := cmdsyncMessageStore{
				localNodeID: 1,
				metas: cmdsyncMessageMetasFake{meta: metadb.ChannelRuntimeMeta{
					ChannelID:   id.ID,
					ChannelType: int64(id.Type),
					Leader:      9,
				}, err: nil},
				remote: &cmdsyncMessageRemoteCapture{err: tc.err},
			}

			messages, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: id.ID, ChannelType: id.Type}, 1, 10)
			require.NoError(t, err)
			require.Empty(t, messages)
		})
	}
}

func TestNewBuildsCMDSyncRuntimeAndCommittedFanout(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, app.Stop()) })

	require.NotNil(t, app.cmdSyncApp)
	require.NotNil(t, app.cmdSyncProjector)

	dispatcher := messageAppDispatcherForTest(t, app)
	fanout, ok := dispatcher.(committedFanout)
	require.Truef(t, ok, "message dispatcher should be committedFanout, got %T", dispatcher)
	require.Len(t, fanout.subscribers, 2)
	require.Same(t, app.committedDispatcher, fanout.subscribers[0])
	require.Same(t, app.cmdSyncProjector, fanout.subscribers[1])
}

type cmdsyncMessageMetasFake struct {
	meta metadb.ChannelRuntimeMeta
	err  error
}

func (f cmdsyncMessageMetasFake) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return f.meta, f.err
}

type cmdsyncMessageRemoteCapture struct {
	nodeIDs  []uint64
	requests []node.ChannelMessagesQuery
	page     node.ChannelMessagesPage
	err      error
}

func (f *cmdsyncMessageRemoteCapture) QueryChannelMessages(_ context.Context, nodeID uint64, req node.ChannelMessagesQuery) (node.ChannelMessagesPage, error) {
	f.nodeIDs = append(f.nodeIDs, nodeID)
	f.requests = append(f.requests, req)
	if f.err != nil {
		return node.ChannelMessagesPage{}, f.err
	}
	return f.page, nil
}

func cmdsyncMessageSeqs(messages []channel.Message) []uint64 {
	seqs := make([]uint64, 0, len(messages))
	for _, msg := range messages {
		seqs = append(seqs, msg.MessageSeq)
	}
	return seqs
}

func messageAppDispatcherForTest(t *testing.T, app *App) any {
	t.Helper()
	require.NotNil(t, app.messageApp)
	field := reflect.ValueOf(app.messageApp).Elem().FieldByName("dispatcher")
	if !field.IsValid() {
		t.Fatal("message.App is missing dispatcher field")
	}
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface()
}
