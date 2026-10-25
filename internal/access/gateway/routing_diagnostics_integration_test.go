//go:build integration

package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type routingDiagnosticError struct {
	cause    error
	snapshot channelappend.RoutingFailureDiagnostics
}

func (e *routingDiagnosticError) Error() string { return e.cause.Error() }
func (e *routingDiagnosticError) Unwrap() error { return e.cause }
func (e *routingDiagnosticError) RoutingFailureDiagnostics() channelappend.RoutingFailureDiagnostics {
	return e.snapshot
}

type routingDiagnosticSubmitter struct{ errors map[string]error }

func (s routingDiagnosticSubmitter) Send(_ context.Context, cmd message.SendCommand) (message.SendResult, error) {
	return message.SendResult{}, s.errors[cmd.ClientMsgNo]
}
func (s routingDiagnosticSubmitter) SendBatch(items []message.SendBatchItem) []message.SendBatchItemResult {
	results := make([]message.SendBatchItemResult, len(items))
	for i := range items {
		results[i].Err = s.errors[items[i].Command.ClientMsgNo]
	}
	return results
}

// This boundary forwards the real usecase and changes the lower carrier only
// after its result is final, before the gateway consumes that result.
type routingDiagnosticMessageBoundary struct {
	app        *message.App
	beforeEmit func(int, message.SendBatchItemResult)
}

func (b routingDiagnosticMessageBoundary) SendBatchEach(items []message.SendBatchItem, emit func(int, message.SendBatchItemResult) error) error {
	return b.app.SendBatchEach(items, func(index int, result message.SendBatchItemResult) error {
		b.beforeEmit(index, result)
		return emit(index, result)
	})
}

func routingDiagnosticValue(n int) channelappend.RoutingFailureDiagnostics {
	return channelappend.RoutingFailureDiagnostics{
		Resolve: time.Duration(n) * time.Millisecond, GroupAdmission: time.Duration(n+1) * time.Millisecond,
		AppendAdmission: time.Duration(n+2) * time.Millisecond, LocalSubmit: time.Duration(n+3) * time.Millisecond,
		LocalWait: time.Duration(n+4) * time.Millisecond, Forward: time.Duration(n+5) * time.Millisecond,
		RetryDelay: time.Duration(n+6) * time.Millisecond, Attempts: n, LocalAttempts: n + 1, RemoteAttempts: n + 2,
	}
}

func TestGatewayRoutingDiagnosticsPreserveTimeoutSnapshotAndACK(t *testing.T) {
	for _, tc := range []struct {
		name     string
		observed bool
		timeout  bool
		value    channelappend.RoutingFailureDiagnostics
	}{
		{name: "measured", observed: true, timeout: true, value: routingDiagnosticValue(2)},
		{name: "observed-zero", observed: true, timeout: true},
		{name: "unobserved-timeout", timeout: true},
		{name: "unobserved-busy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runGatewayRoutingDiagnostics(t, []channelappend.RoutingFailureDiagnostics{tc.value}, tc.observed, tc.timeout)
		})
	}
}

func TestGatewayRoutingDiagnosticsKeepOriginalItemOwnership(t *testing.T) {
	runGatewayRoutingDiagnostics(t, []channelappend.RoutingFailureDiagnostics{routingDiagnosticValue(2), routingDiagnosticValue(7)}, true, true)
}

func runGatewayRoutingDiagnostics(t *testing.T, values []channelappend.RoutingFailureDiagnostics, observed, timeout bool) {
	t.Helper()
	causes := make([]error, len(values))
	carriers := make([]*routingDiagnosticError, len(values))
	errorsByItem := make(map[string]error, len(values))
	expectedText := make([]string, len(values))
	logger := newRecordingLogger("internal.access.gateway")
	var writes []frame.Frame
	sess := newTestSession(t, &writes)
	sess.SetValue(coregateway.SessionValueUID, "sender")
	items := make([]coregateway.SendBatchItem, len(values))
	for i, value := range values {
		key := fmt.Sprintf("routing-item-%d", i)
		if timeout {
			causes[i] = fmt.Errorf("router outcome %d: %w: %w", i, context.DeadlineExceeded, channelappend.ErrAppendNotSubmitted)
		} else {
			causes[i] = fmt.Errorf("router admission %d: %w", i, message.ErrBackpressured)
		}
		err := causes[i]
		if observed {
			carriers[i] = &routingDiagnosticError{cause: err, snapshot: value}
			err = carriers[i]
		}
		errorsByItem[key] = fmt.Errorf("sender boundary: %w", fmt.Errorf("routing boundary: %w", err))
		items[i] = coregateway.SendBatchItem{
			Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
			Frame:   &frame.SendPacket{ClientSeq: uint64(i + 1), ClientMsgNo: key, ChannelID: "group", ChannelType: 2, Payload: []byte("one")},
		}
	}
	app := message.New(message.Options{Submitter: routingDiagnosticSubmitter{errors: errorsByItem}})
	boundary := routingDiagnosticMessageBoundary{app: app, beforeEmit: func(index int, result message.SendBatchItemResult) {
		if index < 0 || index >= len(causes) || result.Err == nil || !errors.Is(result.Err, causes[index]) {
			t.Fatal("message result lost its original item/error identity")
		}
		expectedText[index] = result.Err.Error()
		if carriers[index] != nil {
			carriers[index].snapshot = routingDiagnosticValue(99)
		}
	}}
	handler := New(Options{Logger: logger, SendTimeout: time.Second, Messages: boundary})
	if err := handler.OnSendBatch(items); err != nil {
		t.Fatal(err)
	}
	if len(writes) != len(items) {
		t.Fatalf("ACK count=%d want=%d", len(writes), len(items))
	}
	entries := map[string]recordedLogEntry{}
	for _, entry := range logger.entries() {
		if entry.level != "WARN" || entry.module != "internal.access.gateway.frame" {
			continue
		}
		for _, field := range entry.fields {
			if field.Key == "event" && field.Value == "internal.access.gateway.send_failed" {
				key := requireFieldValue[string](t, entry, "clientMsgNo")
				if _, exists := entries[key]; exists {
					t.Fatal("duplicate original-item warning")
				}
				entries[key] = entry
			}
		}
	}
	if len(entries) != len(items) {
		t.Fatalf("warning count=%d want=%d", len(entries), len(items))
	}
	for i, value := range values {
		key := items[i].Frame.ClientMsgNo
		ack := requireSendack(t, writes, i)
		wantReason := frame.ReasonSystemBusy
		if timeout {
			wantReason = frame.ReasonNodeNotMatch
		}
		if ack.ClientSeq != items[i].Frame.ClientSeq || ack.ClientMsgNo != key || ack.ReasonCode != wantReason {
			t.Fatalf("original ACK identity/order/reason changed: %+v", ack)
		}
		entry, ok := entries[key]
		if !ok {
			t.Fatal("original item warning missing")
		}
		logged := requireFieldValue[error](t, entry, "error")
		if logged.Error() != expectedText[i] || !errors.Is(logged, causes[i]) || errors.Is(logged, context.DeadlineExceeded) != timeout || errors.Is(logged, channelappend.ErrAppendNotSubmitted) != timeout {
			t.Fatal("gateway changed original error text, timeout identity or non-submission proof")
		}
		if !observed {
			for _, field := range entry.fields {
				if strings.HasPrefix(field.Key, "routing") {
					t.Errorf("absent routing observation emitted as %s", field.Key)
				}
			}
			continue
		}
		if !requireFieldValue[bool](t, entry, "routingTimingsPresent") {
			t.Fatal("measured routing snapshot is absent")
		}
		for field, want := range map[string]time.Duration{
			"routingResolveDuration": value.Resolve, "routingGroupAdmissionDuration": value.GroupAdmission,
			"routingAppendAdmissionDuration": value.AppendAdmission, "routingLocalSubmitDuration": value.LocalSubmit,
			"routingLocalWaitDuration": value.LocalWait, "routingForwardDuration": value.Forward,
			"routingRetryDelayDuration": value.RetryDelay,
		} {
			if got := requireFieldValue[time.Duration](t, entry, field); got != want {
				t.Errorf("%s=%v want=%v (snapshot ownership/alignment)", field, got, want)
			}
		}
		for field, want := range map[string]int64{
			"routingAttempts": int64(value.Attempts), "routingLocalAttempts": int64(value.LocalAttempts), "routingRemoteAttempts": int64(value.RemoteAttempts),
		} {
			if got := requireFieldValue[int64](t, entry, field); got != want {
				t.Errorf("%s=%d want=%d", field, got, want)
			}
		}
	}
}
