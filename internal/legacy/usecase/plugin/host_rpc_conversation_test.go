package plugin

import (
	"context"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestHostRPCConversationChannelsMapsAuthoritativeReaderResult(t *testing.T) {
	reader := &recordingConversationReader{channels: []channel.ChannelID{
		{ID: "g1", Type: frame.ChannelTypeGroup},
		{ID: "u2", Type: frame.ChannelTypePerson},
	}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Conversations: reader})

	resp, err := app.ConversationChannels(context.Background(), &pluginproto.ConversationChannelReq{Uid: "u1"}, "plugin.conversation")

	if err != nil {
		t.Fatalf("ConversationChannels returned error: %v", err)
	}
	if reader.calls != 1 || reader.uid != "u1" || reader.limit != 1000 {
		t.Fatalf("conversation reader calls=%d uid=%q limit=%d", reader.calls, reader.uid, reader.limit)
	}
	got := resp.GetChannels()
	if len(got) != 2 || got[0].GetChannelId() != "g1" || got[0].GetChannelType() != uint32(frame.ChannelTypeGroup) || got[1].GetChannelId() != "u2" || got[1].GetChannelType() != uint32(frame.ChannelTypePerson) {
		t.Fatalf("channels = %#v", got)
	}
}

func TestHostRPCConversationChannelsRequiresUIDAndReader(t *testing.T) {
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore()})
	_, err := app.ConversationChannels(context.Background(), &pluginproto.ConversationChannelReq{Uid: "u1"}, "plugin.conversation")
	assertErrorIs(t, err, ErrConversationReaderRequired)

	app = mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Conversations: &recordingConversationReader{}})
	_, err = app.ConversationChannels(context.Background(), &pluginproto.ConversationChannelReq{}, "plugin.conversation")
	assertErrorIs(t, err, ErrConversationUIDRequired)
}

func TestHostRPCConversationChannelsPropagatesReaderError(t *testing.T) {
	reader := &recordingConversationReader{err: errConversationReaderFailed}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Conversations: reader})

	_, err := app.ConversationChannels(context.Background(), &pluginproto.ConversationChannelReq{Uid: "u1"}, "plugin.conversation")

	if err != errConversationReaderFailed {
		t.Fatalf("error = %v, want %v", err, errConversationReaderFailed)
	}
}

func TestHostRPCConversationChannelsKeepsReaderOrder(t *testing.T) {
	reader := &recordingConversationReader{channels: []channel.ChannelID{
		{ID: "z", Type: 2},
		{ID: "a", Type: 1},
		{ID: "m", Type: 2},
	}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Conversations: reader})

	resp, err := app.ConversationChannels(context.Background(), &pluginproto.ConversationChannelReq{Uid: "u1"}, "plugin.conversation")

	if err != nil {
		t.Fatalf("ConversationChannels returned error: %v", err)
	}
	got := make([]channel.ChannelID, 0, len(resp.GetChannels()))
	for _, item := range resp.GetChannels() {
		got = append(got, channel.ChannelID{ID: item.GetChannelId(), Type: uint8(item.GetChannelType())})
	}
	if !reflect.DeepEqual(got, reader.channels) {
		t.Fatalf("channels = %#v, want reader order %#v", got, reader.channels)
	}
}

var errConversationReaderFailed = &conversationReaderError{}

type conversationReaderError struct{}

func (*conversationReaderError) Error() string { return "conversation reader failed" }

type recordingConversationReader struct {
	calls    int
	uid      string
	limit    int
	channels []channel.ChannelID
	err      error
}

func (r *recordingConversationReader) ConversationChannels(_ context.Context, uid string, limit int) ([]channel.ChannelID, error) {
	r.calls++
	r.uid = uid
	r.limit = limit
	if r.err != nil {
		return nil, r.err
	}
	return append([]channel.ChannelID(nil), r.channels...), nil
}
