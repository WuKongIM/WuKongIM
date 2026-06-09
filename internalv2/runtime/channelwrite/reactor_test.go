package channelwrite

import (
	"context"
	"testing"
	"time"
)

func TestReactorRunsPrepareAsWorkerEffect(t *testing.T) {
	authorizer := newBlockingAuthorizerForTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID:       1,
		MessageID:         newSequenceIDsForPrepare(500),
		Authorizer:        authorizer,
		EffectWorkerCount: 1,
	})
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, ChannelKey: "2:room", LeaderNodeID: 1}

	submitC := make(chan submitResult, 1)
	go func() {
		future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
			sendItemForPrepare(validPrepareCommand("u1", "room", "payload")),
		})
		submitC <- submitResult{future: future, err: err}
	}()

	authorizer.waitStarted(t)
	result := receiveSubmitResult(t, submitC)
	if result.err != nil {
		t.Fatalf("SubmitLocal() error = %v", result.err)
	}
	if result.future == nil {
		t.Fatalf("future is nil")
	}

	notDoneCtx, cancelNotDone := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelNotDone()
	if _, err := result.future.Wait(notDoneCtx); err != context.DeadlineExceeded {
		t.Fatalf("Future.Wait() before effect release error = %v, want context deadline", err)
	}

	authorizer.release()
	waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	results, err := result.future.Wait(waitCtx)
	if err != nil {
		t.Fatalf("Future.Wait() error = %v", err)
	}
	requireNotAppended(t, results, 0)
}

type blockingAuthorizerForTest struct {
	started  chan struct{}
	releaseC chan struct{}
}

func newBlockingAuthorizerForTest() *blockingAuthorizerForTest {
	return &blockingAuthorizerForTest{
		started:  make(chan struct{}),
		releaseC: make(chan struct{}),
	}
}

func (a *blockingAuthorizerForTest) AuthorizeSend(context.Context, SendCommand) (Decision, error) {
	close(a.started)
	<-a.releaseC
	return Decision{Allowed: true, Reason: ReasonSuccess}, nil
}

func (a *blockingAuthorizerForTest) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-a.started:
	case <-time.After(time.Second):
		t.Fatalf("authorizer did not start")
	}
}

func (a *blockingAuthorizerForTest) release() {
	close(a.releaseC)
}
