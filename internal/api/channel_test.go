package api

import (
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type recordingConversationDeleter struct {
	calls []conversationDeleteCall
	err   error
}

type conversationDeleteCall struct {
	uid      string
	channel  string
	chType   uint8
	channels []wkdb.Channel
}

func (r *recordingConversationDeleter) DeleteFromCache(uid string, channelId string, channelType uint8) error {
	r.calls = append(r.calls, conversationDeleteCall{uid: uid, channel: channelId, chType: channelType})
	return r.err
}

type recordingConversationStore struct {
	calls []conversationDeleteCall
	err   error
}

func (r *recordingConversationStore) DeleteConversations(uid string, channels []wkdb.Channel) error {
	r.calls = append(r.calls, conversationDeleteCall{uid: uid, channels: channels})
	return r.err
}

func TestDeleteChannelConversationsUsesBatchDeletePerUid(t *testing.T) {
	cache := &recordingConversationDeleter{}
	store := &recordingConversationStore{}

	err := deleteChannelConversations(cache, store, "channel-1", 2, []string{"u1", "u2"})
	if err != nil {
		t.Fatalf("deleteChannelConversations returned error: %v", err)
	}

	if got, want := len(cache.calls), 2; got != want {
		t.Fatalf("cache calls = %d, want %d", got, want)
	}
	if got, want := len(store.calls), 2; got != want {
		t.Fatalf("store calls = %d, want %d", got, want)
	}

	wantChannels := []wkdb.Channel{{ChannelId: "channel-1", ChannelType: 2}}
	for i, uid := range []string{"u1", "u2"} {
		if cache.calls[i].uid != uid || cache.calls[i].channel != "channel-1" || cache.calls[i].chType != 2 {
			t.Fatalf("cache call %d = %+v, want uid %q channel %q type %d", i, cache.calls[i], uid, "channel-1", 2)
		}
		if store.calls[i].uid != uid {
			t.Fatalf("store call %d uid = %q, want %q", i, store.calls[i].uid, uid)
		}
		if !reflect.DeepEqual(store.calls[i].channels, wantChannels) {
			t.Fatalf("store call %d channels = %+v, want %+v", i, store.calls[i].channels, wantChannels)
		}
	}
}

func TestDeleteChannelConversationsNoopsForLiveChannel(t *testing.T) {
	cache := &recordingConversationDeleter{}
	store := &recordingConversationStore{}

	err := deleteChannelConversations(cache, store, "channel-1", wkproto.ChannelTypeLive, []string{"u1", "u2"})
	if err != nil {
		t.Fatalf("deleteChannelConversations returned error: %v", err)
	}
	if got := len(cache.calls); got != 0 {
		t.Fatalf("cache calls = %d, want 0", got)
	}
	if got := len(store.calls); got != 0 {
		t.Fatalf("store calls = %d, want 0", got)
	}
}

func TestDeleteChannelConversationsStopsOnCacheError(t *testing.T) {
	cache := &recordingConversationDeleter{err: errors.New("cache failed")}
	store := &recordingConversationStore{}

	err := deleteChannelConversations(cache, store, "channel-1", 2, []string{"u1", "u2"})
	if err == nil || err.Error() != "cache failed" {
		t.Fatalf("deleteChannelConversations error = %v, want cache failed", err)
	}
	if got, want := len(cache.calls), 1; got != want {
		t.Fatalf("cache calls = %d, want %d", got, want)
	}
	if got := len(store.calls); got != 0 {
		t.Fatalf("store calls = %d, want 0", got)
	}
}

func TestDeleteChannelConversationsStopsOnStoreError(t *testing.T) {
	cache := &recordingConversationDeleter{}
	store := &recordingConversationStore{err: errors.New("store failed")}

	err := deleteChannelConversations(cache, store, "channel-1", 2, []string{"u1", "u2"})
	if err == nil || err.Error() != "store failed" {
		t.Fatalf("deleteChannelConversations error = %v, want store failed", err)
	}
	if got, want := len(cache.calls), 1; got != want {
		t.Fatalf("cache calls = %d, want %d", got, want)
	}
	if got, want := len(store.calls), 1; got != want {
		t.Fatalf("store calls = %d, want %d", got, want)
	}
}
