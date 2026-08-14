package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type recordedLogEntry struct {
	level  string
	module string
	msg    string
	fields []wklog.Field
}

type recordingLoggerSink struct {
	entries []recordedLogEntry
}

type recordingLogger struct {
	module string
	base   []wklog.Field
	sink   *recordingLoggerSink
}

func newRecordingLogger(module string) *recordingLogger {
	return &recordingLogger{module: module, sink: &recordingLoggerSink{}}
}

func (r *recordingLogger) Debug(msg string, fields ...wklog.Field) { r.log("DEBUG", msg, fields...) }
func (r *recordingLogger) Info(msg string, fields ...wklog.Field)  { r.log("INFO", msg, fields...) }
func (r *recordingLogger) Warn(msg string, fields ...wklog.Field)  { r.log("WARN", msg, fields...) }
func (r *recordingLogger) Error(msg string, fields ...wklog.Field) { r.log("ERROR", msg, fields...) }
func (r *recordingLogger) Fatal(msg string, fields ...wklog.Field) { r.log("FATAL", msg, fields...) }

func (r *recordingLogger) Named(name string) wklog.Logger {
	module := name
	if r.module != "" && name != "" {
		module = r.module + "." + name
	}
	return &recordingLogger{module: module, base: append([]wklog.Field(nil), r.base...), sink: r.sink}
}

func (r *recordingLogger) With(fields ...wklog.Field) wklog.Logger {
	merged := append(append([]wklog.Field(nil), r.base...), fields...)
	return &recordingLogger{module: r.module, base: merged, sink: r.sink}
}

func (r *recordingLogger) Sync() error { return nil }

func (r *recordingLogger) log(level, msg string, fields ...wklog.Field) {
	all := append(append([]wklog.Field(nil), r.base...), fields...)
	r.sink.entries = append(r.sink.entries, recordedLogEntry{
		level:  level,
		module: r.module,
		msg:    msg,
		fields: all,
	})
}

func (r *recordingLogger) entries() []recordedLogEntry {
	return append([]recordedLogEntry(nil), r.sink.entries...)
}

func TestHandlerLogsListenerAndSessionErrors(t *testing.T) {
	logger := newRecordingLogger("internal.access.gateway")
	handler := New(Options{Logger: logger})
	listenerErr := errors.New("accept failed")
	sessionErr := errors.New("decode failed")
	sess := newTestSession(t, nil)
	sess.SetValue(coregateway.SessionValueUID, "u1")

	handler.OnListenerError("tcp", listenerErr)
	handler.OnSessionError(coregateway.Context{Session: sess}, sessionErr)

	requireLogEntry(t, logger, "ERROR", "internal.access.gateway.conn", "internal.access.gateway.listener_error")
	sessionEntry := requireLogEntry(t, logger, "WARN", "internal.access.gateway.conn", "internal.access.gateway.session_error")
	if got := requireFieldValue[string](t, sessionEntry, "uid"); got != "u1" {
		t.Fatalf("uid field = %q, want u1", got)
	}
}

func TestHandlerLogsPresenceAndDeliveryCleanupFailures(t *testing.T) {
	logger := newRecordingLogger("internal.access.gateway")
	presenceErr := errors.New("presence cleanup failed")
	deliveryErr := errors.New("delivery cleanup failed")
	sess := newTestSession(t, nil)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	handler := New(Options{
		Logger:   logger,
		Presence: &recordingPresence{deactivateErr: presenceErr, touchErr: presenceErr},
		Delivery: &recordingDelivery{closedErr: deliveryErr, recvackErr: deliveryErr},
	})

	_ = handler.OnSessionClose(coregateway.Context{Session: sess, RequestContext: context.Background()})
	handler.OnSessionActivateRollback(coregateway.Context{Session: sess, RequestContext: context.Background()}, errors.New("connack write failed"))
	handler.touchPresence(&coregateway.Context{Session: sess, RequestContext: context.Background()}, time.Unix(100, 0))
	_ = handler.handleRecvack(&coregateway.Context{Session: sess, RequestContext: context.Background()}, &frame.RecvackPacket{MessageID: 1})

	requireLogEntry(t, logger, "WARN", "internal.access.gateway.conn", "internal.access.gateway.session_close_presence_failed")
	requireLogEntry(t, logger, "WARN", "internal.access.gateway.conn", "internal.access.gateway.session_close_delivery_failed")
	requireLogEntry(t, logger, "WARN", "internal.access.gateway.conn", "internal.access.gateway.activation_rollback_failed")
	requireLogEntry(t, logger, "WARN", "internal.access.gateway.frame", "internal.access.gateway.presence_touch_failed")
	requireLogEntry(t, logger, "WARN", "internal.access.gateway.frame", "internal.access.gateway.recvack_failed")
}

func TestOnSendBatchLogsResultCountMismatch(t *testing.T) {
	logger := newRecordingLogger("internal.access.gateway")
	var written []frame.Frame
	sess := newTestSession(t, &written)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	handler := New(Options{
		Logger: logger,
		Messages: &recordingMessages{
			batchResults: []message.SendBatchItemResult{
				{Result: message.SendResult{Reason: message.ReasonSuccess}},
				{Result: message.SendResult{Reason: message.ReasonSuccess}},
			},
		},
	})

	err := handler.OnSendBatch([]coregateway.SendBatchItem{
		{
			Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
			Frame:   &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "a", ChannelID: "ch1", ChannelType: 2, Payload: []byte("one")},
		},
	})
	if !errors.Is(err, ErrSendBatchResultCountMismatch) {
		t.Fatalf("OnSendBatch() error = %v, want %v", err, ErrSendBatchResultCountMismatch)
	}
	requireLogEntry(t, logger, "ERROR", "internal.access.gateway.frame", "internal.access.gateway.send_batch_result_count_mismatch")
}

func TestOnSendBatchTimeoutLogCarriesConsumedStageBudget(t *testing.T) {
	logger := newRecordingLogger("internal.access.gateway")
	var written []frame.Frame
	sess := newTestSession(t, &written)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	handler := New(Options{
		Logger: logger, SendTimeout: 100 * time.Millisecond,
		Messages: message.New(message.Options{
			PersonDirectory: gatewayDelayedPersonDirectory{delay: 20 * time.Millisecond},
			Submitter:       gatewayDeadlineSubmitter{},
		}),
	})

	err := handler.OnSendBatch([]coregateway.SendBatchItem{{
		Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
		Frame: &frame.SendPacket{
			ClientSeq: 1, ClientMsgNo: "timeout", ChannelID: "u2", ChannelType: 1, Payload: []byte("one"),
		},
	}})
	if err != nil {
		t.Fatalf("OnSendBatch() error = %v", err)
	}
	entry := requireLogEntry(t, logger, "WARN", "internal.access.gateway.frame", "internal.access.gateway.send_failed")
	if stage := requireFieldValue[string](t, entry, "failedStage"); stage != "submitter" {
		t.Fatalf("failed stage = %q, want submitter", stage)
	}
	if duration := requireFieldValue[time.Duration](t, entry, "preAppendDuration"); duration < 15*time.Millisecond {
		t.Fatalf("pre-append duration = %v, want delayed directory cost", duration)
	}
	if budget := requireFieldValue[time.Duration](t, entry, "deadlineBudgetBeforeSubmit"); budget <= 0 || budget >= 95*time.Millisecond {
		t.Fatalf("deadline budget before submit = %v, want consumed positive budget", budget)
	}
}

func TestHandlerWarnsForCanceledWorkBeforePlannedShutdown(t *testing.T) {
	logger := newRecordingLogger("internal.access.gateway")
	handler := New(Options{Logger: logger})

	handler.logSendFailure(message.SendCommand{
		FromUID: "u1", ChannelID: "u2", ChannelType: 1, ClientMsgNo: "runtime-cancel",
	}, sendackSourceBatchResultError, sendackErrorClassCanceled, context.Canceled)

	entry := requireLogEntry(t, logger, "WARN", "internal.access.gateway.frame", "internal.access.gateway.send_failed")
	if class := requireFieldValue[string](t, entry, "errorClass"); class != sendackErrorClassCanceled {
		t.Fatalf("canceled send error class = %q, want %q", class, sendackErrorClassCanceled)
	}
}

func TestHandlerSuppressesCanceledWorkAfterPlannedShutdownFence(t *testing.T) {
	logger := newRecordingLogger("internal.access.gateway")
	handler := New(Options{Logger: logger})
	handler.BeginPlannedShutdown()

	handler.logSendFailure(message.SendCommand{
		FromUID: "u1", ChannelID: "u2", ChannelType: 1, ClientMsgNo: "planned-stop",
	}, sendackSourceBatchResultError, sendackErrorClassCanceled, context.Canceled)

	if entries := logger.entries(); len(entries) != 0 {
		t.Fatalf("planned canceled send log entries = %#v, want none", entries)
	}
}

type gatewayDelayedPersonDirectory struct{ delay time.Duration }

func (d gatewayDelayedPersonDirectory) EnsurePersonChannelDirectory(ctx context.Context, _ string, _ int64) error {
	timer := time.NewTimer(d.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type gatewayDeadlineSubmitter struct{}

func (gatewayDeadlineSubmitter) Send(context.Context, message.SendCommand) (message.SendResult, error) {
	return message.SendResult{}, context.DeadlineExceeded
}

func (gatewayDeadlineSubmitter) SendBatch(items []message.SendBatchItem) []message.SendBatchItemResult {
	results := make([]message.SendBatchItemResult, len(items))
	for index := range results {
		results[index].Err = context.DeadlineExceeded
	}
	return results
}

func requireLogEntry(t *testing.T, logger *recordingLogger, level, module, event string) recordedLogEntry {
	t.Helper()
	for _, entry := range logger.entries() {
		if entry.level != level || entry.module != module {
			continue
		}
		for _, field := range entry.fields {
			if field.Key == "event" && field.Value == event {
				return entry
			}
		}
	}
	t.Fatalf("missing log entry level=%s module=%s event=%s entries=%#v", level, module, event, logger.entries())
	return recordedLogEntry{}
}

func requireFieldValue[T any](t *testing.T, entry recordedLogEntry, key string) T {
	t.Helper()
	for _, field := range entry.fields {
		if field.Key != key {
			continue
		}
		value, ok := field.Value.(T)
		if !ok {
			t.Fatalf("field %q type = %T, want requested type", key, field.Value)
		}
		return value
	}
	var zero T
	t.Fatalf("missing field %q in %#v", key, entry.fields)
	return zero
}
