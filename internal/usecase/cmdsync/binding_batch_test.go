package cmdsync

import (
	"context"
	"errors"
	"fmt"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"strings"
	"testing"
	"time"

	channelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestBatchBindCapturesOneTailAndNormalizesRecipients(t *testing.T) {
	s := newCmdSyncStore()
	key := CommandChannelKey{ChannelID: "group____cmd", ChannelType: 2}
	s.tails[key] = 41
	a := New(Options{States: s, Messages: s, Now: func() time.Time { return time.Unix(0, 99) }})
	if err := a.Bind(context.Background(), BindCommand{UIDs: []string{" u1 ", "u2", "u1"}, ChannelID: "group", ChannelType: 2}); err != nil {
		t.Fatal(err)
	}
	if len(s.tailCalls) != 1 || len(s.upserts) != 2 {
		t.Fatalf("tails=%d writes=%d", len(s.tailCalls), len(s.upserts))
	}
	for i, r := range s.upserts {
		if r.UID != fmt.Sprintf("u%d", i+1) || r.CommandChannelID != key.ChannelID || r.StartSeq != 42 || r.UpdatedAt != 99 {
			t.Fatalf("row=%+v", r)
		}
	}
	if err := a.Unbind(context.Background(), UnbindCommand{UIDs: []string{"u1", "u2"}, ChannelID: "group", ChannelType: 2}); err != nil {
		t.Fatal(err)
	}
	if len(s.tailCalls) != 1 || len(s.tombstones) != 2 {
		t.Fatalf("unbind reads a tail or loses targets")
	}
}

func TestScopedBindUsesExactlyTheSendChannelCodec(t *testing.T) {
	subscribers := []string{" u2 ", "u1", "u2", ""}
	codec := channelid.CommandCodec{Suffix: "__custom"}
	expected, err := codec.RequestSubscriberChannelFor(subscribers)
	if err != nil {
		t.Fatal(err)
	}
	s := newCmdSyncStore()
	a := New(Options{States: s, Messages: s, CommandChannelSuffix: "__custom"})
	if err := a.Bind(context.Background(), BindCommand{Subscribers: subscribers}); err != nil {
		t.Fatal(err)
	}
	if len(s.upserts) != 2 || len(s.tailCalls) != 1 {
		t.Fatalf("writes=%d tails=%d", len(s.upserts), len(s.tailCalls))
	}
	for i, r := range s.upserts {
		if r.UID != expected.Subscribers[i] || r.CommandChannelID != expected.CommandChannelID || r.ChannelType != int64(expected.ChannelType) {
			t.Fatalf("bound a different scope: %+v", r)
		}
	}
}

func TestBatchBindingRejectsConflictsAndBoundsBeforeIO(t *testing.T) {
	huge := make([]string, 1001)
	for i := range huge {
		huge[i] = "u"
	}
	for _, cmd := range []BindCommand{
		{UID: "u", UIDs: []string{"v"}, ChannelID: "g", ChannelType: 2},
		{Subscribers: []string{"u"}, ChannelID: "g", ChannelType: 2},
		{UIDs: huge, ChannelID: "g", ChannelType: 2},
		{Subscribers: huge},
		{UIDs: []string{" "}, ChannelID: "g", ChannelType: 2},
	} {
		s := newCmdSyncStore()
		a := New(Options{States: s, Messages: s})
		if err := a.Bind(context.Background(), cmd); err == nil {
			t.Fatalf("accepted invalid binding %+v", cmd)
		}
		if len(s.tailCalls) != 0 || len(s.upserts) != 0 {
			t.Fatal("invalid binding performed IO")
		}
	}
}

// Errors must stop the caller before it sends a command with incomplete discovery.
func TestBatchBindingPropagatesFailureAndCancellation(t *testing.T) {
	sentinel := errors.New("binding storage unavailable")
	for _, stage := range []string{"tail", "write", "unbind", "cancel"} {
		t.Run(stage, func(t *testing.T) {
			s := &bindingFailureStore{cmdSyncStore: newCmdSyncStore(), stage: stage, err: sentinel}
			a := New(Options{States: s, Messages: s})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := sentinel
			if stage == "cancel" {
				cancel()
				want = context.Canceled
			}
			var err error
			if stage == "unbind" {
				err = a.Unbind(ctx, UnbindCommand{Subscribers: []string{"u1", "u2"}})
			} else {
				err = a.Bind(ctx, BindCommand{Subscribers: []string{"u1", "u2"}})
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
			if (stage == "tail" || stage == "cancel") && s.writes != 0 {
				t.Fatal("wrote after failed precondition")
			}
		})
	}
}

type bindingFailureStore struct {
	*cmdSyncStore
	stage  string
	err    error
	writes int
}

func (s *bindingFailureStore) CommandChannelTail(ctx context.Context, k CommandChannelKey) (uint64, error) {
	if s.stage == "tail" {
		return 0, s.err
	}
	return s.cmdSyncStore.CommandChannelTail(ctx, k)
}
func (s *bindingFailureStore) UpsertUserCMDChannelMemberships(_ context.Context, rows []metadb.UserCMDChannelMembership) error {
	s.writes++
	return s.err
}
func (s *bindingFailureStore) TombstoneUserCMDChannelMemberships(_ context.Context, rows []metadb.UserCMDChannelMembership) error {
	s.writes++
	return s.err
}

func TestBindingIdentityBytesBoundBeforeIO(t *testing.T) {
	a := New(Options{})
	if err := a.Bind(context.Background(), BindCommand{Subscribers: []string{strings.Repeat("x", 256*1024), "y"}}); !errors.Is(err, ErrBindingTargets) {
		t.Fatalf("got %v", err)
	}
}
