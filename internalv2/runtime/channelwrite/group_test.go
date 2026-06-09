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

func TestSubmitLocalCanceledBeforeSubmitNeverEnqueues(t *testing.T) {
	for i := 0; i < 256; i++ {
		group := New(Options{LocalNodeID: 1})
		if err := group.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 1}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		future, err := group.SubmitLocal(ctx, target, []SendBatchItem{testSendItem("u1", "room")})
		stateCount := group.StateCountForTest()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		stopErr := group.Stop(stopCtx)
		stopCancel()
		if stopErr != nil {
			t.Fatalf("Stop() error = %v", stopErr)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: SubmitLocal() error = %v, want context.Canceled", i, err)
		}
		if future != nil {
			t.Fatalf("iteration %d: future = %v, want nil", i, future)
		}
		if stateCount != 0 {
			t.Fatalf("iteration %d: canceled submit created %d states", i, stateCount)
		}
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

func TestSubmitLocalIgnoresCallerCancellationAfterMailboxAcceptance(t *testing.T) {
	group := newStartedTestGroup(t, Options{LocalNodeID: 1, MailboxSize: 1})
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 1}
	reactor := group.reactorForTarget(target)
	release := blockReactorForTest(t, reactor)

	ctx, cancel := context.WithCancel(context.Background())
	resultC := make(chan submitResult, 1)
	go func() {
		future, err := group.SubmitLocal(ctx, target, []SendBatchItem{testSendItem("u1", "room")})
		resultC <- submitResult{future: future, err: err}
	}()

	waitForMailboxLen(t, reactor, 1)
	cancel()
	close(release)

	result := receiveSubmitResult(t, resultC)
	if result.err != nil {
		t.Fatalf("SubmitLocal() error = %v, want nil after mailbox acceptance", result.err)
	}
	if result.future == nil {
		t.Fatalf("future is nil")
	}
}

func TestSubmitLocalFutureCompletesWithNotAppendedResults(t *testing.T) {
	group := newStartedTestGroup(t, Options{LocalNodeID: 1})
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 1}
	items := []SendBatchItem{
		testSendItem("u1", "room"),
		testSendItem("u2", "room"),
	}

	future, err := group.SubmitLocal(context.Background(), target, items)
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results, err := future.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if len(results) != len(items) {
		t.Fatalf("results len = %d, want %d", len(results), len(items))
	}
	for i, result := range results {
		if !errors.Is(result.Err, ErrNotAppended) {
			t.Fatalf("results[%d].Err = %v, want ErrNotAppended", i, result.Err)
		}
	}
}

func TestStopTimeoutClosesAdmissionWhileSubmitWaitsForAck(t *testing.T) {
	group := New(Options{LocalNodeID: 1, MailboxSize: 1})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 1}
	reactor := group.reactorForTarget(target)
	release := blockReactorForTest(t, reactor)
	defer closeReleaseForTest(&release)

	submitC := make(chan submitResult, 1)
	go func() {
		future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u1", "room")})
		submitC <- submitResult{future: future, err: err}
	}()
	waitForMailboxLen(t, reactor, 1)

	stopC := make(chan error, 1)
	go func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		stopC <- group.Stop(stopCtx)
	}()
	select {
	case err := <-stopC:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Stop() error = %v, want context deadline", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("Stop() did not observe context while submit waited for ack")
	}
	if err := group.Start(context.Background()); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("Start() error = %v, want ErrBackpressured", err)
	}
	if _, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u2", "room")}); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("SubmitLocal() error = %v, want ErrBackpressured", err)
	}

	closeReleaseForTest(&release)
	result := receiveSubmitResult(t, submitC)
	if result.err != nil {
		t.Fatalf("admitted SubmitLocal() error = %v", result.err)
	}
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := group.Stop(drainCtx); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestStopTimeoutKeepsAdmissionClosedAndAllowsLaterDrain(t *testing.T) {
	group := New(Options{LocalNodeID: 1})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 1}
	release := blockReactorForTest(t, group.reactorForTarget(target))

	stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelStop()
	if err := group.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want context deadline", err)
	}
	if err := group.Start(context.Background()); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("Start() error = %v, want ErrBackpressured", err)
	}
	if _, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u1", "room")}); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("SubmitLocal() error = %v, want ErrBackpressured", err)
	}

	close(release)
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := group.Stop(drainCtx); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if err := group.Start(context.Background()); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("Start() after drain error = %v, want ErrBackpressured", err)
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

type submitResult struct {
	future *Future
	err    error
}

type blockingReactorEvent struct {
	entered chan struct{}
	release <-chan struct{}
}

func (e blockingReactorEvent) apply(*reactor) {
	close(e.entered)
	<-e.release
}

func blockReactorForTest(t *testing.T, reactor *reactor) chan struct{} {
	t.Helper()
	entered := make(chan struct{})
	release := make(chan struct{})
	reactor.mailbox <- blockingReactorEvent{entered: entered, release: release}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatalf("reactor did not enter blocking event")
	}
	return release
}

func closeReleaseForTest(release *chan struct{}) {
	if *release == nil {
		return
	}
	close(*release)
	*release = nil
}

func waitForMailboxLen(t *testing.T, reactor *reactor, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if len(reactor.mailbox) == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("reactor mailbox len = %d, want %d", len(reactor.mailbox), want)
		case <-tick.C:
		}
	}
}

func receiveSubmitResult(t *testing.T, resultC <-chan submitResult) submitResult {
	t.Helper()
	select {
	case result := <-resultC:
		return result
	case <-time.After(time.Second):
		t.Fatalf("SubmitLocal did not return")
		return submitResult{}
	}
}
