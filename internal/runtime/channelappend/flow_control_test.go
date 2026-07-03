package channelappend

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHighWatermarkRejectsBeforeAppend(t *testing.T) {
	appender := newBlockingAppenderForAppendTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                 1,
		MessageID:                   newSequenceIDsForPrepare(600),
		ChannelBacklogHighWatermark: 1,
		Appender:                    appender,
	})
	target := localTargetForAppendTest("room")

	firstC := submitNoWaitForAppendTest(group, target, appendSendItemForTest("u1", "room", "first"))
	firstStart := appender.waitStarted(t)

	second, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{appendSendItemForTest("u2", "room", "second")})
	if err == nil && second != nil {
		waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
		results, waitErr := second.Wait(waitCtx)
		cancelWait()
		if errors.Is(waitErr, context.DeadlineExceeded) {
			firstStart.Release()
			first := receiveSubmitResult(t, firstC)
			_ = waitFutureForTest(t, first.future)
			t.Fatalf("overflow future did not complete with ErrChannelBusy")
		}
		err = waitErr
		if err == nil && len(results) == 1 {
			err = results[0].Err
		}
	}
	if !errors.Is(err, ErrChannelBusy) {
		firstStart.Release()
		first := receiveSubmitResult(t, firstC)
		_ = waitFutureForTest(t, first.future)
		t.Fatalf("overflow error = %v, want ErrChannelBusy", err)
	}
	if got := appender.Calls(); got > 1 {
		firstStart.Release()
		first := receiveSubmitResult(t, firstC)
		_ = waitFutureForTest(t, first.future)
		t.Fatalf("overflow reached append path, append calls = %d", got)
	}

	firstStart.Release()
	first := receiveSubmitResult(t, firstC)
	requireAppendSuccess(t, waitFutureForTest(t, first.future), 0, 600, 1)
}
