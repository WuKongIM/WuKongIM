//go:build integration

package gateway

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type submissionDiagnosticSender func([]channelappend.SendBatchItem) []channelappend.SendBatchItemResult

func (f submissionDiagnosticSender) SendBatch(items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
	return f(items)
}

// This crosses the real admission owner, message usecase and deferred gateway
// entry. Only the authority sender is gated; no timing internals are inspected.
func TestDeferredGatewayTimeoutReportsOriginalSubmissionPhases(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		blockerEntered, blockerRelease := make(chan struct{}), make(chan struct{})
		routeEntered, routeRelease := make(chan struct{}), make(chan struct{})
		cause := fmt.Errorf("authority wait: %w", context.DeadlineExceeded)
		owner, err := channelappend.NewOrderedSubmitter(channelappend.OrderedSubmitterOptions{
			Workers: 1, Capacity: 2, PayloadCapacity: 128, BatchMaxRecords: 1, BatchMaxBytes: 128,
		}, submissionDiagnosticSender(func(items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
			if items[0].Command.ClientMsgNo == "blocker" {
				close(blockerEntered)
				<-blockerRelease
				return []channelappend.SendBatchItemResult{{}}
			}
			if len(items) != 1 || items[0].Command.ClientMsgNo != "timeout" {
				t.Error("original input alignment changed")
			}
			if deadline, ok := items[0].Context.Deadline(); !ok || !deadline.Equal(items[0].Deadline) {
				t.Error("original caller deadline was not preserved")
			}
			close(routeEntered)
			<-routeRelease
			return []channelappend.SendBatchItemResult{{Err: cause}}
		}))
		if err != nil {
			t.Fatal(err)
		}
		if err := owner.Submit([]channelappend.SendBatchItem{{Context: context.Background(), Command: channelappend.SendCommand{
			FromUID: "u", ChannelID: "blocker", ChannelType: 2, ClientMsgNo: "blocker", Payload: []byte("x"),
		}}}, func([]channelappend.SendBatchItemResult) {}); err != nil {
			t.Fatal(err)
		}
		<-blockerEntered
		logger := newRecordingLogger("internal.access.gateway")
		var writes []frame.Frame
		sess := newTestSession(t, &writes)
		sess.SetValue(coregateway.SessionValueUID, "u")
		handler := New(Options{Logger: logger, SendTimeout: time.Second})
		entry := handler.WithDeferredSends(message.New(message.Options{BatchAdmission: owner})).(coregateway.DeferredSendBatchHandler)
		done := make(chan error, 1)
		var publish func() error
		if err := entry.OnSendBatchDeferred([]coregateway.SendBatchItem{{
			Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
			Frame:   &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "timeout", ChannelID: "g", ChannelType: 2, Payload: []byte("one")},
		}}, func(_ int, fn func() error) error { publish = fn; return nil }, func(err error) { done <- err }); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(3 * time.Millisecond)
		close(blockerRelease)
		<-routeEntered
		synctest.Wait()
		time.Sleep(5 * time.Millisecond)
		close(routeRelease)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := owner.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if publish == nil || publish() != nil || len(writes) != 1 {
			t.Fatal("deferred ACK publication changed")
		}
		if ack := requireSendack(t, writes, 0); ack.ReasonCode != mapReason(message.ReasonNodeNotMatch) {
			t.Fatalf("timeout reason changed: %v", ack.ReasonCode)
		}
		entryLog := requireLogEntry(t, logger, "WARN", "internal.access.gateway.frame", "internal.access.gateway.send_failed")
		if !requireFieldValue[bool](t, entryLog, "submissionTimingsPresent") {
			t.Fatal("owned submission timings absent")
		}
		for key, want := range map[string]time.Duration{
			"submissionQueueDuration":       3 * time.Millisecond,
			"submissionDependencyDuration":  0,
			"submissionReadyDuration":       3 * time.Millisecond,
			"submissionRouteDuration":       5 * time.Millisecond,
			"submissionPreCallbackDuration": 0,
			"submitterDuration":             8 * time.Millisecond,
		} {
			if got := requireFieldValue[time.Duration](t, entryLog, key); got != want {
				t.Errorf("%s = %v, want %v", key, got, want)
			}
		}
		if !errors.Is(requireFieldValue[error](t, entryLog, "error"), context.DeadlineExceeded) {
			t.Fatal("timeout identity lost across diagnostics")
		}
	})
}

type submissionDiagnosticRejectedAdmission struct{}

func (submissionDiagnosticRejectedAdmission) Submit([]message.SendBatchItem, func([]message.SendBatchItemResult)) error {
	return context.DeadlineExceeded
}

func TestDeferredGatewayTimeoutWithoutSubmissionOwnerDoesNotReportZeroPhases(t *testing.T) {
	logger := newRecordingLogger("internal.access.gateway")
	var writes []frame.Frame
	sess := newTestSession(t, &writes)
	sess.SetValue(coregateway.SessionValueUID, "u")
	handler := New(Options{Logger: logger, SendTimeout: time.Second})
	entry := handler.WithDeferredSends(message.New(message.Options{BatchAdmission: submissionDiagnosticRejectedAdmission{}})).(coregateway.DeferredSendBatchHandler)
	done := make(chan error, 1)
	if err := entry.OnSendBatchDeferred([]coregateway.SendBatchItem{{
		Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
		Frame:   &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "unowned", ChannelID: "g", ChannelType: 2, Payload: []byte("one")},
	}}, func(_ int, publish func() error) error { return publish() }, func(err error) { done <- err }); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	entryLog := requireLogEntry(t, logger, "WARN", "internal.access.gateway.frame", "internal.access.gateway.send_failed")
	for _, field := range entryLog.fields {
		switch field.Key {
		case "submissionTimingsPresent", "submissionQueueDuration", "submissionDependencyDuration", "submissionReadyDuration", "submissionRouteDuration", "submissionPreCallbackDuration":
			t.Errorf("unobserved submission phase reported as a measurement: %s", field.Key)
		}
	}
	if requireFieldValue[string](t, entryLog, "failedStage") != "submitter" || !errors.Is(requireFieldValue[error](t, entryLog, "error"), context.DeadlineExceeded) {
		t.Fatal("existing unowned timeout diagnostics changed")
	}
}
