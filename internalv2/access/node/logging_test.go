package node

import (
	"context"
	"errors"
	"testing"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type recordedNodeLogEntry struct {
	level  string
	module string
	fields []wklog.Field
}

type recordingNodeLogger struct {
	module  string
	base    []wklog.Field
	entries *[]recordedNodeLogEntry
}

func newRecordingNodeLogger(module string) *recordingNodeLogger {
	entries := make([]recordedNodeLogEntry, 0)
	return &recordingNodeLogger{module: module, entries: &entries}
}

func (r *recordingNodeLogger) Debug(msg string, fields ...wklog.Field) { r.log("DEBUG", fields...) }
func (r *recordingNodeLogger) Info(msg string, fields ...wklog.Field)  { r.log("INFO", fields...) }
func (r *recordingNodeLogger) Warn(msg string, fields ...wklog.Field)  { r.log("WARN", fields...) }
func (r *recordingNodeLogger) Error(msg string, fields ...wklog.Field) { r.log("ERROR", fields...) }
func (r *recordingNodeLogger) Fatal(msg string, fields ...wklog.Field) { r.log("FATAL", fields...) }

func (r *recordingNodeLogger) Named(name string) wklog.Logger {
	module := name
	if r.module != "" && name != "" {
		module = r.module + "." + name
	}
	return &recordingNodeLogger{module: module, base: append([]wklog.Field(nil), r.base...), entries: r.entries}
}

func (r *recordingNodeLogger) With(fields ...wklog.Field) wklog.Logger {
	base := append(append([]wklog.Field(nil), r.base...), fields...)
	return &recordingNodeLogger{module: r.module, base: base, entries: r.entries}
}

func (r *recordingNodeLogger) Sync() error { return nil }

func (r *recordingNodeLogger) log(level string, fields ...wklog.Field) {
	all := append(append([]wklog.Field(nil), r.base...), fields...)
	*r.entries = append(*r.entries, recordedNodeLogEntry{level: level, module: r.module, fields: all})
}

func TestPresenceRPCLogsDecodeAndRejectedErrors(t *testing.T) {
	logger := newRecordingNodeLogger("internalv2.access.node")
	authority := newFakePresenceAuthority()
	authority.registerErr = errors.New("store unavailable")
	adapter := New(Options{Authority: authority, Logger: logger})

	if _, err := adapter.HandlePresenceAuthorityRPC(context.Background(), []byte("bad")); err == nil {
		t.Fatal("HandlePresenceAuthorityRPC() error = nil, want decode error")
	}
	requireNodeLogEntry(t, logger, "WARN", "internalv2.access.node.rpc", "internalv2.access.node.presence_authority_decode_failed")

	body, err := encodePresenceRPCRequestBinary(presenceRPCRequest{
		Op:     presenceOpRegisterRoute,
		Target: testPresenceTarget(),
		Route:  testPresenceRoute("u1", 101),
	})
	if err != nil {
		t.Fatalf("encodePresenceRPCRequestBinary() error = %v", err)
	}
	respBody, err := adapter.HandlePresenceAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePresenceAuthorityRPC() error = %v", err)
	}
	resp, err := decodePresenceRPCResponse(respBody)
	if err != nil {
		t.Fatalf("decodePresenceRPCResponse() error = %v", err)
	}
	if resp.Status != rpcStatusRejected {
		t.Fatalf("status = %q, want %q", resp.Status, rpcStatusRejected)
	}
	requireNodeLogEntry(t, logger, "WARN", "internalv2.access.node.rpc", "internalv2.access.node.presence_authority_rejected")
}

func TestDeliveryRPCLogsRejectedPushAndFanout(t *testing.T) {
	logger := newRecordingNodeLogger("internalv2.access.node")
	pushErr := errors.New("owner push failed")
	fanoutErr := errors.New("fanout failed")
	adapter := New(Options{
		Logger:         logger,
		Delivery:       &fakeDeliveryOwnerPush{err: pushErr},
		DeliveryFanout: &fakeDeliveryFanoutRunner{err: fanoutErr},
	})

	pushBody, err := encodeDeliveryPushRequest(deliveryPushRequest{Command: testDeliveryPushCommand()})
	if err != nil {
		t.Fatalf("encodeDeliveryPushRequest() error = %v", err)
	}
	if _, err := adapter.HandleDeliveryPushRPC(context.Background(), pushBody); err != nil {
		t.Fatalf("HandleDeliveryPushRPC() error = %v", err)
	}
	requireNodeLogEntry(t, logger, "WARN", "internalv2.access.node.rpc", "internalv2.access.node.delivery_push_rejected")

	fanoutBody, err := encodeDeliveryFanoutRequest(deliveryFanoutRequest{Task: runtimedelivery.FanoutTask{Envelope: testDeliveryPushCommand().Envelope}})
	if err != nil {
		t.Fatalf("encodeDeliveryFanoutRequest() error = %v", err)
	}
	if _, err := adapter.HandleDeliveryFanoutRPC(context.Background(), fanoutBody); err != nil {
		t.Fatalf("HandleDeliveryFanoutRPC() error = %v", err)
	}
	requireNodeLogEntry(t, logger, "WARN", "internalv2.access.node.rpc", "internalv2.access.node.delivery_fanout_rejected")
}

func requireNodeLogEntry(t *testing.T, logger *recordingNodeLogger, level, module, event string) recordedNodeLogEntry {
	t.Helper()
	for _, entry := range *logger.entries {
		if entry.level != level || entry.module != module {
			continue
		}
		for _, field := range entry.fields {
			if field.Key == "event" && field.Value == event {
				return entry
			}
		}
	}
	t.Fatalf("missing log entry level=%s module=%s event=%s entries=%#v", level, module, event, *logger.entries)
	return recordedNodeLogEntry{}
}
