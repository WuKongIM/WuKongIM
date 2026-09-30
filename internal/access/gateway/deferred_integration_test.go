//go:build integration

package gateway

import (
	"context"
	"errors"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"testing"
	"time"
)

type deferredMessagesProbe struct {
	items    []message.SendBatchItem
	emit     func(int, message.SendBatchItemResult) error
	complete func(error)
	reject   error
	calls    int
}

func (p *deferredMessagesProbe) SubmitBatchEach(items []message.SendBatchItem, emit func(int, message.SendBatchItemResult) error, complete func(error)) error {
	p.calls++
	if p.reject != nil {
		return p.reject
	}
	p.items = items
	p.emit = emit
	p.complete = complete
	return nil
}
func deferredEntry(t *testing.T, h *Handler, p *deferredMessagesProbe) coregateway.DeferredSendBatchHandler {
	t.Helper()
	value, ok := h.WithDeferredSends(p).(coregateway.DeferredSendBatchHandler)
	if !ok {
		t.Fatal("deferred entry missing")
	}
	return value
}
func TestDeferredGatewayMapsResultsOnlyAtOrderedPublication(t *testing.T) {
	p := &deferredMessagesProbe{}
	var writes []frame.Frame
	var metas []session.OutboundMeta
	sess := newTestSessionWithMeta(t, &writes, &metas)
	sess.SetValue(coregateway.SessionValueUID, "alice")
	h := New(Options{OwnerNodeID: 7, SendTimeout: time.Minute})
	entry := deferredEntry(t, h, p)
	items := []coregateway.SendBatchItem{{Context: coregateway.Context{Session: sess, RequestContext: context.Background()}, ReplyToken: "r1", Frame: &frame.SendPacket{ClientMsgNo: "one", ClientSeq: 11, ChannelID: "bob", ChannelType: frame.ChannelTypePerson, Payload: []byte("one")}}, {Context: coregateway.Context{Session: sess, RequestContext: context.Background()}, ReplyToken: "r2", Frame: &frame.SendPacket{ClientMsgNo: "two", ClientSeq: 12, ChannelID: "g", ChannelType: frame.ChannelTypeGroup}}}
	publish := make(map[int]func() error)
	done := make(chan error, 1)
	if err := entry.OnSendBatchDeferred(items, func(i int, f func() error) error { publish[i] = f; return nil }, func(err error) { done <- err }); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 || len(p.items) != 2 || p.items[0].Command.FromUID != "alice" || p.items[0].Command.SenderNodeID != 7 || !p.items[0].Command.NormalizePersonChannel {
		t.Fatal("command mapping changed")
	}
	if p.items[0].Deadline.IsZero() || !p.items[0].Deadline.Equal(p.items[1].Deadline) {
		t.Fatal("batch deadline lost")
	}
	if err := p.emit(1, message.SendBatchItemResult{Result: message.SendResult{Reason: message.ReasonSendBan}}); err != nil {
		t.Fatal(err)
	}
	if err := p.emit(0, message.SendBatchItemResult{Result: message.SendResult{Reason: message.ReasonSuccess, MessageID: 101, MessageSeq: 8}}); err != nil {
		t.Fatal(err)
	}
	p.complete(nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(writes) != 0 {
		t.Fatal("ACK bypassed core publication")
	}
	for _, i := range []int{0, 1} {
		if err := publish[i](); err != nil {
			t.Fatal(err)
		}
	}
	if ack := requireSendack(t, writes, 0); ack.ClientMsgNo != "one" || ack.ClientSeq != 11 || ack.MessageID != 101 || ack.MessageSeq != 8 {
		t.Fatal(ack)
	}
	if ack := requireSendack(t, writes, 1); ack.ReasonCode != mapReason(message.ReasonSendBan) {
		t.Fatal(ack)
	}
	if len(metas) != 2 || metas[0].ReplyToken != "r1" || metas[1].ReplyToken != "r2" {
		t.Fatal(metas)
	}
}
func TestDeferredGatewayPrechecksAndEmptyBatch(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "prechecks", true: "empty"}[empty], func(t *testing.T) {
			p := &deferredMessagesProbe{}
			var writes []frame.Frame
			anonymous := newTestSession(t, &writes)
			authenticated := newTestSession(t, &writes)
			authenticated.SetValue(coregateway.SessionValueUID, "u")
			items := []coregateway.SendBatchItem{{Context: coregateway.Context{Session: anonymous, RequestContext: context.Background()}, Frame: &frame.SendPacket{}}, {Context: coregateway.Context{Session: authenticated}, Frame: &frame.SendPacket{}}}
			if empty {
				items = nil
			}
			done := make(chan error, 1)
			var callbacks []func() error
			err := deferredEntry(t, New(Options{}), p).OnSendBatchDeferred(items, func(_ int, f func() error) error { callbacks = append(callbacks, f); return nil }, func(err error) { done <- err })
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if p.calls != 0 || len(callbacks) != len(items) || len(writes) != 0 {
				t.Fatal("precheck ownership mismatch")
			}
			for _, f := range callbacks {
				if err := f(); err != nil {
					t.Fatal(err)
				}
			}
			if !empty && (requireSendack(t, writes, 0).ReasonCode != mapReason(message.ReasonAuthFail) || requireSendack(t, writes, 1).ReasonCode != mapReason(message.ReasonSystemError)) {
				t.Fatal(writes)
			}
		})
	}
}
func TestDeferredGatewayRejectsMalformedEmissionAndAdmission(t *testing.T) {
	for _, mode := range []string{"missing", "duplicate", "index", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			p := &deferredMessagesProbe{}
			sentinel := errors.New("admission failure")
			if mode == "rejected" {
				p.reject = sentinel
			}
			var writes []frame.Frame
			sess := newTestSession(t, &writes)
			sess.SetValue(coregateway.SessionValueUID, "u")
			done := make(chan error, 1)
			count := 0
			err := deferredEntry(t, New(Options{}), p).OnSendBatchDeferred([]coregateway.SendBatchItem{{Context: coregateway.Context{Session: sess, RequestContext: context.Background()}, Frame: &frame.SendPacket{}}}, func(int, func() error) error { count++; return nil }, func(err error) { done <- err })
			if mode == "rejected" {
				if !errors.Is(err, sentinel) {
					t.Fatal(err)
				}
				select {
				case <-done:
					t.Fatal("completion after admission rejection")
				default:
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "duplicate":
				if err := p.emit(0, message.SendBatchItemResult{}); err != nil {
					t.Fatal(err)
				}
				p.complete(p.emit(0, message.SendBatchItemResult{}))
			case "index":
				p.complete(p.emit(2, message.SendBatchItemResult{}))
			default:
				p.complete(nil)
			}
			if err := <-done; !errors.Is(err, ErrSendBatchResultCountMismatch) {
				t.Fatal(err)
			}
			if count > 1 {
				t.Fatal("duplicate publication")
			}
		})
	}
}
func TestDeferredGatewayPublicationHonorsTerminalSeal(t *testing.T) {
	p := &deferredMessagesProbe{}
	var writes []frame.Frame
	sess := newTestSession(t, &writes)
	sess.SetValue(coregateway.SessionValueUID, "u")
	h := New(Options{})
	entry := deferredEntry(t, h, p)
	var write func() error
	if err := entry.OnSendBatchDeferred([]coregateway.SendBatchItem{{Context: coregateway.Context{Session: sess, RequestContext: context.Background()}, Frame: &frame.SendPacket{}}}, func(_ int, f func() error) error { write = f; return nil }, func(error) {}); err != nil {
		t.Fatal(err)
	}
	if err := p.emit(0, message.SendBatchItemResult{}); err != nil {
		t.Fatal(err)
	}
	p.complete(nil)
	ctx := coregateway.Context{Session: sess}
	if err := ctx.SealOutboundAndWrite(&frame.PongPacket{}); err != nil {
		t.Fatal(err)
	}
	if err := write(); err == nil {
		t.Fatal("ACK crossed terminal seal")
	}
	if len(writes) != 1 {
		t.Fatal(writes)
	}
	if h.WithDeferredSends(nil) != h {
		t.Fatal("nil port replaced joined handler")
	}
}
