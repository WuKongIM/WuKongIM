package channelwrite

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSubmitLocalCreatesStateOnlyForLocalAuthority(t *testing.T) {
	group := newStartedTestGroup(t, Options{LocalNodeID: 1})
	target := AuthorityTarget{
		ChannelID:    ChannelID{ID: "room", Type: 2},
		ChannelKey:   "2:room",
		LeaderNodeID: 1,
		Epoch:        10,
		LeaderEpoch:  3,
	}
	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u1", "room")})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	if future == nil {
		t.Fatalf("future is nil")
	}
	if !group.HasStateForTest(target.ChannelID) {
		t.Fatalf("authority state was not created")
	}
}

func TestSubmitLocalRejectsRemoteAuthorityWithoutState(t *testing.T) {
	group := newStartedTestGroup(t, Options{LocalNodeID: 1})
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 2}
	_, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u1", "room")})
	if !errors.Is(err, ErrNotChannelAuthority) {
		t.Fatalf("SubmitLocal() error = %v, want ErrNotChannelAuthority", err)
	}
	if group.StateCountForTest() != 0 {
		t.Fatalf("remote authority created state")
	}
}

func TestStartAfterStopKeepsAdmissionClosed(t *testing.T) {
	group := newStartedTestGroup(t, Options{LocalNodeID: 1})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := group.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := group.Start(context.Background()); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("Start() error = %v, want ErrBackpressured", err)
	}
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 1}
	if _, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u1", "room")}); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("SubmitLocal() error = %v, want ErrBackpressured", err)
	}
}

func newStartedTestGroup(t *testing.T, opts Options) *Group {
	t.Helper()

	group := New(opts)
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := group.Stop(ctx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})
	return group
}

func testSendItem(uid, channelID string) SendBatchItem {
	return SendBatchItem{
		Context: context.Background(),
		Command: SendCommand{
			FromUID:     uid,
			ChannelID:   channelID,
			ChannelType: 2,
			ClientMsgNo: uid + "-msg",
			Payload:     []byte("hello"),
		},
	}
}

func (g *Group) HasStateForTest(channelID ChannelID) bool {
	for _, reactor := range g.reactors {
		reactor.mu.Lock()
		_, ok := reactor.states[channelKey(channelID)]
		reactor.mu.Unlock()
		if ok {
			return true
		}
	}
	return false
}

func (g *Group) StateCountForTest() int {
	count := 0
	for _, reactor := range g.reactors {
		reactor.mu.Lock()
		count += len(reactor.states)
		reactor.mu.Unlock()
	}
	return count
}
