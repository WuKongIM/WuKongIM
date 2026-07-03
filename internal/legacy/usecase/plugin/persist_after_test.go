package plugin

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/legacy/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

func TestPersistAfterCommittedInvokesPluginsInPriorityOrderAndMapsMessage(t *testing.T) {
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["async-low"] = ObservedPlugin{No: "async-low", Status: StatusRunning, Enabled: true, Methods: []Method{MethodPersistAfter}, Priority: 1}
	rt.plugins["sync-high"] = ObservedPlugin{No: "sync-high", Status: StatusRunning, Enabled: true, Methods: []Method{MethodPersistAfter}, Priority: 10, PersistAfterSync: true}
	invoker := &persistAfterInvoker{}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: newFakeDesiredStore(), Invoker: invoker})
	msg := channel.Message{
		MessageID:   123,
		MessageSeq:  45,
		ClientMsgNo: "client-1",
		StreamNo:    "stream-1",
		StreamID:    9,
		Timestamp:   1715800000,
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Topic:       "topic-a",
		Payload:     []byte("hello"),
	}

	err := app.PersistAfterCommitted(context.Background(), messageevents.MessageCommitted{Message: msg})

	require.NoError(t, err)
	require.Equal(t, []persistAfterCall{
		{no: "sync-high", sync: true, path: PathPersistAfter},
		{no: "async-low", sync: false, msgType: MsgTypePersistAfter},
	}, invoker.callSummaries())
	batch := decodePersistAfterBatch(t, invoker.calls[0].body)
	require.Len(t, batch.GetMessages(), 1)
	got := batch.GetMessages()[0]
	require.Equal(t, int64(123), got.GetMessageId())
	require.Equal(t, uint64(45), got.GetMessageSeq())
	require.Equal(t, "client-1", got.GetClientMsgNo())
	require.Equal(t, "stream-1", got.GetStreamNo())
	require.Equal(t, uint64(9), got.GetStreamId())
	require.Equal(t, uint32(1715800000), got.GetTimestamp())
	require.Equal(t, "u1", got.GetFrom())
	require.Equal(t, "g1", got.GetChannelId())
	require.Equal(t, uint32(frame.ChannelTypeGroup), got.GetChannelType())
	require.Equal(t, "topic-a", got.GetTopic())
	require.Equal(t, []byte("hello"), got.GetPayload())

	msg.Payload[0] = 'H'
	batch = decodePersistAfterBatch(t, invoker.calls[0].body)
	require.Equal(t, []byte("hello"), batch.GetMessages()[0].GetPayload())
}

func TestPersistAfterCommittedContinuesAndReturnsNonFatalErrors(t *testing.T) {
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["sync-high"] = ObservedPlugin{No: "sync-high", Status: StatusRunning, Enabled: true, Methods: []Method{MethodPersistAfter}, Priority: 10, PersistAfterSync: true}
	rt.plugins["async-low"] = ObservedPlugin{No: "async-low", Status: StatusRunning, Enabled: true, Methods: []Method{MethodPersistAfter}, Priority: 1}
	invoker := &persistAfterInvoker{requestErr: errors.New("sync failed"), sendErr: errors.New("async failed")}
	logger := &recordingPersistAfterLogger{}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: newFakeDesiredStore(), Invoker: invoker, Logger: logger})

	err := app.PersistAfterCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageID: 1, MessageSeq: 1}})

	require.Error(t, err)
	require.Contains(t, err.Error(), "sync failed")
	require.Contains(t, err.Error(), "async failed")
	require.Equal(t, []string{"sync-high", "async-low"}, invoker.pluginNos())
	require.Len(t, logger.warns, 2)
	require.True(t, persistAfterLogHasPlugin(logger.warns[0].fields, "sync-high"))
	require.True(t, persistAfterLogHasPlugin(logger.warns[1].fields, "async-low"))
}

type persistAfterCall struct {
	no      string
	sync    bool
	path    string
	msgType uint32
	body    []byte
}

type persistAfterInvoker struct {
	calls      []persistAfterCall
	requestErr error
	sendErr    error
}

func (p *persistAfterInvoker) RequestPlugin(_ context.Context, no, path string, body []byte) ([]byte, error) {
	p.calls = append(p.calls, persistAfterCall{no: no, sync: true, path: path, body: append([]byte(nil), body...)})
	return nil, p.requestErr
}

func (p *persistAfterInvoker) SendPlugin(no string, msgType uint32, body []byte) error {
	p.calls = append(p.calls, persistAfterCall{no: no, msgType: msgType, body: append([]byte(nil), body...)})
	return p.sendErr
}

func (p *persistAfterInvoker) Stop(context.Context, string) error { return nil }

func (p *persistAfterInvoker) callSummaries() []persistAfterCall {
	out := make([]persistAfterCall, 0, len(p.calls))
	for _, call := range p.calls {
		out = append(out, persistAfterCall{no: call.no, sync: call.sync, path: call.path, msgType: call.msgType})
	}
	return out
}

func (p *persistAfterInvoker) pluginNos() []string {
	out := make([]string, 0, len(p.calls))
	for _, call := range p.calls {
		out = append(out, call.no)
	}
	return out
}

func decodePersistAfterBatch(t *testing.T, body []byte) *pluginproto.MessageBatch {
	t.Helper()
	var batch pluginproto.MessageBatch
	require.NoError(t, batch.Unmarshal(body))
	return &batch
}

type persistAfterLogEntry struct {
	msg    string
	fields []wklog.Field
}

type recordingPersistAfterLogger struct {
	warns []persistAfterLogEntry
}

func (r *recordingPersistAfterLogger) Debug(string, ...wklog.Field) {}
func (r *recordingPersistAfterLogger) Info(string, ...wklog.Field)  {}
func (r *recordingPersistAfterLogger) Error(string, ...wklog.Field) {}
func (r *recordingPersistAfterLogger) Fatal(string, ...wklog.Field) {}

func (r *recordingPersistAfterLogger) Warn(msg string, fields ...wklog.Field) {
	r.warns = append(r.warns, persistAfterLogEntry{msg: msg, fields: append([]wklog.Field(nil), fields...)})
}

func (r *recordingPersistAfterLogger) Named(string) wklog.Logger { return r }
func (r *recordingPersistAfterLogger) With(...wklog.Field) wklog.Logger {
	return r
}
func (r *recordingPersistAfterLogger) Sync() error { return nil }

func persistAfterLogHasPlugin(fields []wklog.Field, pluginNo string) bool {
	for _, field := range fields {
		if field.Key == "pluginNo" && field.Value == pluginNo {
			return true
		}
	}
	return false
}
