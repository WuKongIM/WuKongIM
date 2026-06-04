package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	authoritypresence "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestHandlerOnSessionActivateCallsPresenceActivate(t *testing.T) {
	sess := newTestSession(t, nil)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	sess.SetValue(coregateway.SessionValueDeviceID, "d1")
	sess.SetValue(coregateway.SessionValueDeviceFlag, frame.APP)
	sess.SetValue(coregateway.SessionValueDeviceLevel, frame.DeviceLevelMaster)
	usecase := &recordingPresence{}
	handler := New(Options{Presence: usecase})

	connack, err := handler.OnSessionActivate(&coregateway.Context{
		Session:        sess,
		Listener:       "tcp",
		RequestContext: context.Background(),
	})
	if err != nil {
		t.Fatalf("OnSessionActivate() error = %v", err)
	}
	if connack != nil {
		t.Fatalf("OnSessionActivate() connack = %#v, want nil", connack)
	}
	if len(usecase.activateCommands) != 1 {
		t.Fatalf("activate command count = %d, want 1", len(usecase.activateCommands))
	}
	cmd := usecase.activateCommands[0]
	if cmd.UID != "u1" || cmd.DeviceID != "d1" || cmd.DeviceFlag != uint8(frame.APP) || cmd.DeviceLevel != uint8(frame.DeviceLevelMaster) {
		t.Fatalf("activate identity fields = %#v", cmd)
	}
	if cmd.Listener != "tcp" || cmd.SessionID != sess.ID() || cmd.ConnectedUnix == 0 {
		t.Fatalf("activate route fields = listener:%q session:%d connected:%d", cmd.Listener, cmd.SessionID, cmd.ConnectedUnix)
	}
	if cmd.Session == nil {
		t.Fatalf("activate session handle is nil")
	}
}

func TestHandlerOnSessionActivateRejectsMissingUID(t *testing.T) {
	sess := newTestSession(t, nil)
	usecase := &recordingPresence{}
	handler := New(Options{Presence: usecase})

	_, err := handler.OnSessionActivate(&coregateway.Context{
		Session:        sess,
		RequestContext: context.Background(),
	})
	if !errors.Is(err, ErrUnauthenticatedSession) {
		t.Fatalf("OnSessionActivate() error = %v, want %v", err, ErrUnauthenticatedSession)
	}
	if len(usecase.activateCommands) != 0 {
		t.Fatalf("activate command count = %d, want 0", len(usecase.activateCommands))
	}
}

func TestHandlerOnSessionActivateForwardsContextFallbacksAndErrors(t *testing.T) {
	wantErr := errors.New("activate failed")
	reqCtx := context.WithValue(context.Background(), testContextKey{}, "request")
	sess := session.New(session.Config{ID: 101, Listener: "fallback-listener"})
	sess.SetValue(coregateway.SessionValueUID, "u1")
	usecase := &recordingPresence{activateErr: wantErr}
	handler := New(Options{Presence: usecase})

	connack, err := handler.OnSessionActivate(&coregateway.Context{
		Session:        sess,
		RequestContext: reqCtx,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("OnSessionActivate() error = %v, want %v", err, wantErr)
	}
	if connack != nil {
		t.Fatalf("OnSessionActivate() connack = %#v, want nil", connack)
	}
	if len(usecase.activateContexts) != 1 || usecase.activateContexts[0] != reqCtx {
		t.Fatalf("activate context = %#v, want request context", usecase.activateContexts)
	}
	if len(usecase.activateCommands) != 1 {
		t.Fatalf("activate command count = %d, want 1", len(usecase.activateCommands))
	}
	if got := usecase.activateCommands[0].Listener; got != "fallback-listener" {
		t.Fatalf("activate listener = %q, want fallback listener", got)
	}
}

func TestHandlerOnSessionActivateClassifiesPresenceRouteErrors(t *testing.T) {
	reqCtx := context.WithValue(context.Background(), testContextKey{}, "request")
	sess := session.New(session.Config{ID: 101, Listener: "fallback-listener"})
	sess.SetValue(coregateway.SessionValueUID, "u1")
	usecase := &recordingPresence{activateErr: authoritypresence.ErrRouteNotReady}
	handler := New(Options{Presence: usecase})

	_, err := handler.OnSessionActivate(&coregateway.Context{
		Session:        sess,
		RequestContext: reqCtx,
	})
	if !errors.Is(err, authoritypresence.ErrRouteNotReady) {
		t.Fatalf("OnSessionActivate() error = %v, want %v", err, authoritypresence.ErrRouteNotReady)
	}
	classified, ok := err.(interface{ GatewayAuthFailure() string })
	if !ok {
		t.Fatalf("OnSessionActivate() error does not expose GatewayAuthFailure: %T", err)
	}
	if got := classified.GatewayAuthFailure(); got != "activation_route_not_ready" {
		t.Fatalf("GatewayAuthFailure() = %q, want activation_route_not_ready", got)
	}
}

func TestGatewayPresenceSessionCloseUsesContextCloseHook(t *testing.T) {
	sess := newTestSession(t, nil)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	usecase := &recordingPresence{}
	handler := New(Options{Presence: usecase})
	var closed bool
	var closeReason coregateway.CloseReason
	var closeErr error

	_, err := handler.OnSessionActivate(&coregateway.Context{
		Session:        sess,
		Listener:       "tcp",
		RequestContext: context.Background(),
		CloseSessionFn: func(reason coregateway.CloseReason, err error) {
			closed = true
			closeReason = reason
			closeErr = err
		},
	})
	if err != nil {
		t.Fatalf("OnSessionActivate() error = %v", err)
	}
	if len(usecase.activateCommands) != 1 || usecase.activateCommands[0].Session == nil {
		t.Fatalf("activate command missing session handle: %#v", usecase.activateCommands)
	}

	if err := usecase.activateCommands[0].Session.CloseSession("conflict"); err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	if !closed {
		t.Fatalf("CloseSessionFn was not called")
	}
	if closeReason != coregateway.CloseReasonPolicyViolation {
		t.Fatalf("close reason = %q, want %q", closeReason, coregateway.CloseReasonPolicyViolation)
	}
	if closeErr == nil || closeErr.Error() != "conflict" {
		t.Fatalf("close error = %v, want conflict", closeErr)
	}
}

func TestHandlerOnSessionCloseCallsPresenceDeactivate(t *testing.T) {
	sess := newTestSession(t, nil)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	usecase := &recordingPresence{}
	handler := New(Options{Presence: usecase})

	err := handler.OnSessionClose(coregateway.Context{
		Session:        sess,
		RequestContext: context.Background(),
	})
	if err != nil {
		t.Fatalf("OnSessionClose() error = %v", err)
	}
	if len(usecase.deactivateCommands) != 1 {
		t.Fatalf("deactivate command count = %d, want 1", len(usecase.deactivateCommands))
	}
	cmd := usecase.deactivateCommands[0]
	if cmd.UID != "u1" || cmd.SessionID != sess.ID() {
		t.Fatalf("deactivate command = %#v", cmd)
	}
}

func TestHandlerOnSessionCloseForwardsDeliveryWhenPresenceFails(t *testing.T) {
	sess := newTestSession(t, nil)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	presenceErr := errors.New("presence failed")
	deliveryUsecase := &recordingDelivery{}
	handler := New(Options{
		Presence: &recordingPresence{deactivateErr: presenceErr},
		Delivery: deliveryUsecase,
	})

	err := handler.OnSessionClose(coregateway.Context{
		Session:        sess,
		RequestContext: context.Background(),
	})
	if !errors.Is(err, presenceErr) {
		t.Fatalf("OnSessionClose() error = %v, want joined presence error", err)
	}
	if len(deliveryUsecase.closedCommands) != 1 {
		t.Fatalf("delivery session closed commands = %d, want 1", len(deliveryUsecase.closedCommands))
	}
	cmd := deliveryUsecase.closedCommands[0]
	if cmd.UID != "u1" || cmd.SessionID != sess.ID() {
		t.Fatalf("delivery session closed command = %#v", cmd)
	}
}

func TestHandlerOnSessionActivateRollbackCallsPresenceDeactivate(t *testing.T) {
	sess := newTestSession(t, nil)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	usecase := &recordingPresence{}
	handler := New(Options{Presence: usecase})

	handler.OnSessionActivateRollback(coregateway.Context{
		Session:        sess,
		RequestContext: context.Background(),
	}, errors.New("connack write failed"))
	if len(usecase.deactivateCommands) != 1 {
		t.Fatalf("deactivate command count = %d, want 1", len(usecase.deactivateCommands))
	}
	cmd := usecase.deactivateCommands[0]
	if cmd.UID != "u1" || cmd.SessionID != sess.ID() {
		t.Fatalf("rollback deactivate command = %#v", cmd)
	}
}

func TestOnFrameSendPacketWritesSuccessSendack(t *testing.T) {
	var written []frame.Frame
	sess := newTestSession(t, &written)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	sess.SetValue(coregateway.SessionValueProtocolVersion, uint8(4))

	usecase := &recordingMessages{
		sendResult: message.SendResult{
			MessageID:  9001,
			MessageSeq: 42,
			Reason:     message.ReasonSuccess,
		},
	}
	handler := New(Options{Messages: usecase, SendTimeout: time.Second, OwnerNodeID: 9})
	pkt := &frame.SendPacket{
		Framer: frame.Framer{
			NoPersist: true,
			SyncOnce:  true,
			RedDot:    true,
		},
		ClientSeq:   7,
		ClientMsgNo: "client-1",
		ChannelID:   "ch1",
		ChannelType: 2,
		Payload:     []byte("hello"),
	}

	err := handler.OnFrame(coregateway.Context{
		Session:        sess,
		RequestContext: context.Background(),
	}, pkt)
	if err != nil {
		t.Fatalf("OnFrame() error = %v", err)
	}
	if len(usecase.sendCommands) != 1 {
		t.Fatalf("send command count = %d, want 1", len(usecase.sendCommands))
	}
	cmd := usecase.sendCommands[0]
	if cmd.FromUID != "u1" || cmd.SenderNodeID != 9 || cmd.SenderSessionID != sess.ID() || cmd.ProtocolVersion != 4 {
		t.Fatalf("mapped sender fields = uid=%q node=%d session=%d version=%d", cmd.FromUID, cmd.SenderNodeID, cmd.SenderSessionID, cmd.ProtocolVersion)
	}
	if cmd.ClientSeq != pkt.ClientSeq || cmd.ClientMsgNo != pkt.ClientMsgNo || cmd.ChannelID != pkt.ChannelID || cmd.ChannelType != pkt.ChannelType {
		t.Fatalf("mapped packet fields = %#v, want packet fields from %#v", cmd, pkt)
	}
	if cmd.MessageID != 0 {
		t.Fatalf("gateway-origin MessageID = %d, want 0", cmd.MessageID)
	}
	if !cmd.NoPersist || !cmd.SyncOnce || !cmd.RedDot {
		t.Fatalf("mapped framer flags = noPersist:%v syncOnce:%v redDot:%v", cmd.NoPersist, cmd.SyncOnce, cmd.RedDot)
	}
	pkt.Payload[0] = 'H'
	if string(cmd.Payload) != "hello" {
		t.Fatalf("command payload = %q, want cloned original payload", string(cmd.Payload))
	}

	ack := requireSendack(t, written, 0)
	if ack.ClientSeq != pkt.ClientSeq || ack.ClientMsgNo != pkt.ClientMsgNo {
		t.Fatalf("ack client fields = seq:%d msgNo:%q", ack.ClientSeq, ack.ClientMsgNo)
	}
	if ack.MessageID != int64(usecase.sendResult.MessageID) || ack.MessageSeq != usecase.sendResult.MessageSeq {
		t.Fatalf("ack message fields = id:%d seq:%d", ack.MessageID, ack.MessageSeq)
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("ack reason = %v, want %v", ack.ReasonCode, frame.ReasonSuccess)
	}
}

func TestOnFrameRecvackForwardsToDelivery(t *testing.T) {
	sess := newTestSession(t, nil)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	deliveryUsecase := &recordingDelivery{}
	handler := New(Options{Delivery: deliveryUsecase})

	err := handler.OnFrame(coregateway.Context{
		Session:        sess,
		RequestContext: context.Background(),
	}, &frame.RecvackPacket{MessageID: 77, MessageSeq: 8})
	if err != nil {
		t.Fatalf("OnFrame() error = %v", err)
	}
	if len(deliveryUsecase.recvackCommands) != 1 {
		t.Fatalf("recvack commands = %d, want 1", len(deliveryUsecase.recvackCommands))
	}
	cmd := deliveryUsecase.recvackCommands[0]
	if cmd.UID != "u1" || cmd.SessionID != sess.ID() || cmd.MessageID != 77 || cmd.MessageSeq != 8 {
		t.Fatalf("recvack command = %#v", cmd)
	}
}

func TestOnFrameUnauthenticatedSessionWritesAuthFailSendack(t *testing.T) {
	var written []frame.Frame
	sess := newTestSession(t, &written)
	handler := New(Options{Messages: &recordingMessages{}, SendTimeout: time.Second})
	pkt := &frame.SendPacket{ClientSeq: 8, ClientMsgNo: "client-2", ChannelID: "ch1", ChannelType: 2, Payload: []byte("hello")}

	err := handler.OnFrame(coregateway.Context{
		Session:        sess,
		RequestContext: context.Background(),
	}, pkt)
	if err != nil {
		t.Fatalf("OnFrame() error = %v, want sendack instead of raw error", err)
	}
	ack := requireSendack(t, written, 0)
	if ack.ClientSeq != pkt.ClientSeq || ack.ClientMsgNo != pkt.ClientMsgNo {
		t.Fatalf("ack client fields = seq:%d msgNo:%q", ack.ClientSeq, ack.ClientMsgNo)
	}
	if ack.ReasonCode != frame.ReasonAuthFail {
		t.Fatalf("ack reason = %v, want %v", ack.ReasonCode, frame.ReasonAuthFail)
	}
}

func TestOnFrameNilMessagesWritesSystemErrorSendack(t *testing.T) {
	var written []frame.Frame
	sess := newTestSession(t, &written)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	handler := New(Options{SendTimeout: time.Second})
	pkt := &frame.SendPacket{ClientSeq: 9, ClientMsgNo: "client-3", ChannelID: "ch1", ChannelType: 2, Payload: []byte("hello")}

	err := handler.OnFrame(coregateway.Context{
		Session:        sess,
		RequestContext: context.Background(),
	}, pkt)
	if err != nil {
		t.Fatalf("OnFrame() error = %v, want sendack instead of raw error", err)
	}
	ack := requireSendack(t, written, 0)
	if ack.ClientSeq != pkt.ClientSeq || ack.ClientMsgNo != pkt.ClientMsgNo {
		t.Fatalf("ack client fields = seq:%d msgNo:%q", ack.ClientSeq, ack.ClientMsgNo)
	}
	if ack.ReasonCode != frame.ReasonSystemError {
		t.Fatalf("ack reason = %v, want %v", ack.ReasonCode, frame.ReasonSystemError)
	}
}

func TestOnFrameMapsMessageErrorsToSendackReasons(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want frame.ReasonCode
	}{
		{name: "route not ready", err: message.ErrRouteNotReady, want: frame.ReasonNodeNotMatch},
		{name: "not leader", err: message.ErrNotLeader, want: frame.ReasonNodeNotMatch},
		{name: "stale route", err: message.ErrStaleRoute, want: frame.ReasonNodeNotMatch},
		{name: "channel missing", err: message.ErrChannelNotFound, want: frame.ReasonChannelNotExist},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var written []frame.Frame
			sess := newTestSession(t, &written)
			sess.SetValue(coregateway.SessionValueUID, "u1")
			handler := New(Options{Messages: &recordingMessages{sendErr: tt.err}, SendTimeout: time.Second})
			pkt := &frame.SendPacket{ClientSeq: 10, ClientMsgNo: "client-error", ChannelID: "ch1", ChannelType: 2, Payload: []byte("hello")}

			err := handler.OnFrame(coregateway.Context{
				Session:        sess,
				RequestContext: context.Background(),
			}, pkt)
			if err != nil {
				t.Fatalf("OnFrame() error = %v", err)
			}
			ack := requireSendack(t, written, 0)
			if ack.ReasonCode != tt.want {
				t.Fatalf("ack reason = %v, want %v", ack.ReasonCode, tt.want)
			}
		})
	}
}

func TestOnFramePingPacketWritesPong(t *testing.T) {
	var written []frame.Frame
	var metas []session.OutboundMeta
	sess := newTestSessionWithMeta(t, &written, &metas)
	handler := New(Options{Messages: &recordingMessages{}, SendTimeout: time.Second})

	err := handler.OnFrame(coregateway.Context{Session: sess, ReplyToken: "ping-1", RequestContext: context.Background()}, &frame.PingPacket{})
	if err != nil {
		t.Fatalf("OnFrame() error = %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("written frame count = %d, want 1", len(written))
	}
	if _, ok := written[0].(*frame.PongPacket); !ok {
		t.Fatalf("written[0] = %T, want *frame.PongPacket", written[0])
	}
	if len(metas) != 1 || metas[0].ReplyToken != "ping-1" {
		t.Fatalf("pong reply token metas = %#v, want ping-1", metas)
	}
}

func TestHandlerPingTouchesPresenceBeforePong(t *testing.T) {
	var written []frame.Frame
	presenceUsecase := &recordingPresence{}
	sess := session.New(session.Config{
		ID: 99,
		WriteFrameFn: func(f frame.Frame, _ session.OutboundMeta) error {
			if len(presenceUsecase.touchCommands) != 1 {
				t.Fatalf("touch command count before pong write = %d, want 1", len(presenceUsecase.touchCommands))
			}
			written = append(written, f)
			return nil
		},
	})
	handler := New(Options{Presence: presenceUsecase})

	err := handler.OnFrame(coregateway.Context{Session: sess, RequestContext: context.Background()}, &frame.PingPacket{})
	if err != nil {
		t.Fatalf("OnFrame() error = %v", err)
	}
	if len(presenceUsecase.touchCommands) != 1 {
		t.Fatalf("touch command count = %d, want 1", len(presenceUsecase.touchCommands))
	}
	if got := presenceUsecase.touchCommands[0].SessionID; got != 99 {
		t.Fatalf("touch session id = %d, want 99", got)
	}
	if got := presenceUsecase.touchCommands[0].ActivityUnix; got <= 0 {
		t.Fatalf("touch activity unix = %d, want positive", got)
	}
	if len(written) != 1 {
		t.Fatalf("written frame count = %d, want 1", len(written))
	}
	if _, ok := written[0].(*frame.PongPacket); !ok {
		t.Fatalf("written[0] = %T, want *frame.PongPacket", written[0])
	}
}

func TestHandlerPingStillWritesPongWhenTouchFails(t *testing.T) {
	var written []frame.Frame
	sess := session.New(session.Config{
		ID: 99,
		WriteFrameFn: func(f frame.Frame, _ session.OutboundMeta) error {
			written = append(written, f)
			return nil
		},
	})
	presenceUsecase := &recordingPresence{touchErr: presence.ErrLocalRegistryUnavailable}
	handler := New(Options{Presence: presenceUsecase})

	err := handler.OnFrame(coregateway.Context{Session: sess, RequestContext: context.Background()}, &frame.PingPacket{})
	if err != nil {
		t.Fatalf("OnFrame() error = %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("written frame count = %d, want 1", len(written))
	}
	if _, ok := written[0].(*frame.PongPacket); !ok {
		t.Fatalf("written[0] = %T, want *frame.PongPacket", written[0])
	}
}

func TestOnFrameUnknownFrameReturnsErrUnsupportedFrame(t *testing.T) {
	handler := New(Options{Messages: &recordingMessages{}, SendTimeout: time.Second})

	err := handler.OnFrame(coregateway.Context{RequestContext: context.Background()}, &frame.PongPacket{})
	if !errors.Is(err, ErrUnsupportedFrame) {
		t.Fatalf("OnFrame() error = %v, want %v", err, ErrUnsupportedFrame)
	}
}

func TestOnSendBatchWritesAlignedSendacks(t *testing.T) {
	var written []frame.Frame
	var metas []session.OutboundMeta
	sess := newTestSessionWithMeta(t, &written, &metas)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	usecase := &recordingMessages{
		batchResults: []message.SendBatchItemResult{
			{Result: message.SendResult{MessageID: 11, MessageSeq: 101, Reason: message.ReasonSuccess}},
			{Result: message.SendResult{Reason: message.ReasonNodeNotMatch}},
		},
	}
	handler := New(Options{Messages: usecase, SendTimeout: time.Second, OwnerNodeID: 9})
	items := []coregateway.SendBatchItem{
		{
			Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
			Frame:   &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "a", ChannelID: "ch1", ChannelType: 2, Payload: []byte("one")},
		},
		{
			Context:    coregateway.Context{Session: sess, RequestContext: context.Background()},
			ReplyToken: "reply-2",
			Frame:      &frame.SendPacket{ClientSeq: 2, ClientMsgNo: "b", ChannelID: "ch1", ChannelType: 2, Payload: []byte("two")},
		},
	}

	err := handler.OnSendBatch(items)
	if err != nil {
		t.Fatalf("OnSendBatch() error = %v", err)
	}
	if len(usecase.batchItems) != 2 {
		t.Fatalf("batch item count = %d, want 2", len(usecase.batchItems))
	}
	if usecase.batchItems[0].Command.ClientMsgNo != "a" || usecase.batchItems[1].Command.ClientMsgNo != "b" {
		t.Fatalf("batch commands not aligned: %#v", usecase.batchItems)
	}
	if usecase.batchItems[0].Command.SenderNodeID != 9 || usecase.batchItems[1].Command.SenderNodeID != 9 {
		t.Fatalf("batch sender node ids = %d,%d want 9", usecase.batchItems[0].Command.SenderNodeID, usecase.batchItems[1].Command.SenderNodeID)
	}
	if usecase.batchItems[1].Context == nil {
		t.Fatalf("batch item context with reply token is nil")
	}

	first := requireSendack(t, written, 0)
	if first.ClientSeq != 1 || first.ClientMsgNo != "a" || first.MessageID != 11 || first.MessageSeq != 101 || first.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("first ack = %#v", first)
	}
	second := requireSendack(t, written, 1)
	if second.ClientSeq != 2 || second.ClientMsgNo != "b" || second.ReasonCode != frame.ReasonNodeNotMatch {
		t.Fatalf("second ack = %#v", second)
	}
	if got := metas[1].ReplyToken; got != "reply-2" {
		t.Fatalf("second reply token = %q, want reply-2", got)
	}
}

func TestOnSendBatchPassesSharedDeadlineWithoutReplacingRequestContext(t *testing.T) {
	var written []frame.Frame
	sess := newTestSession(t, &written)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	usecase := &recordingMessages{
		batchResults: []message.SendBatchItemResult{
			{Result: message.SendResult{MessageID: 1, MessageSeq: 1, Reason: message.ReasonSuccess}},
			{Result: message.SendResult{MessageID: 2, MessageSeq: 2, Reason: message.ReasonSuccess}},
		},
	}
	handler := New(Options{Messages: usecase, SendTimeout: time.Minute})

	err := handler.OnSendBatch([]coregateway.SendBatchItem{
		{
			Context: coregateway.Context{Session: sess, RequestContext: reqCtx},
			Frame:   &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "a", ChannelID: "ch1", ChannelType: 2, Payload: []byte("one")},
		},
		{
			Context: coregateway.Context{Session: sess, RequestContext: reqCtx},
			Frame:   &frame.SendPacket{ClientSeq: 2, ClientMsgNo: "b", ChannelID: "ch1", ChannelType: 2, Payload: []byte("two")},
		},
	})
	if err != nil {
		t.Fatalf("OnSendBatch() error = %v", err)
	}
	if got := len(usecase.batchItems); got != 2 {
		t.Fatalf("batch items = %d, want 2", got)
	}
	if usecase.batchItems[0].Context != reqCtx || usecase.batchItems[1].Context != reqCtx {
		t.Fatalf("batch request context was replaced")
	}
	if usecase.batchItems[0].Deadline.IsZero() || !usecase.batchItems[0].Deadline.Equal(usecase.batchItems[1].Deadline) {
		t.Fatalf("batch deadlines = %v and %v, want one shared non-zero deadline", usecase.batchItems[0].Deadline, usecase.batchItems[1].Deadline)
	}
}

func TestOnSendBatchReusesFramePayloadUntilMessageBoundary(t *testing.T) {
	var written []frame.Frame
	sess := newTestSession(t, &written)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	payload := []byte("one")
	usecase := &recordingMessages{
		batchResults: []message.SendBatchItemResult{
			{Result: message.SendResult{MessageID: 1, MessageSeq: 1, Reason: message.ReasonSuccess}},
		},
	}
	handler := New(Options{Messages: usecase, SendTimeout: time.Second})

	err := handler.OnSendBatch([]coregateway.SendBatchItem{
		{
			Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
			Frame:   &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "a", ChannelID: "ch1", ChannelType: 2, Payload: payload},
		},
	})
	if err != nil {
		t.Fatalf("OnSendBatch() error = %v", err)
	}
	if got := len(usecase.batchItems); got != 1 {
		t.Fatalf("batch items = %d, want 1", got)
	}
	if len(usecase.batchItems[0].Command.Payload) == 0 || &usecase.batchItems[0].Command.Payload[0] != &payload[0] {
		t.Fatalf("batch command payload was cloned before message append boundary")
	}
}

func TestOnSendBatchMapsMessageErrorsToSendackReasons(t *testing.T) {
	var written []frame.Frame
	sess := newTestSession(t, &written)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	usecase := &recordingMessages{
		batchResults: []message.SendBatchItemResult{
			{Err: message.ErrRouteNotReady},
			{Err: message.ErrNotLeader},
			{Err: message.ErrChannelNotFound},
		},
	}
	handler := New(Options{Messages: usecase, SendTimeout: time.Second})

	err := handler.OnSendBatch([]coregateway.SendBatchItem{
		{
			Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
			Frame:   &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "a", ChannelID: "ch1", ChannelType: 2, Payload: []byte("one")},
		},
		{
			Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
			Frame:   &frame.SendPacket{ClientSeq: 2, ClientMsgNo: "b", ChannelID: "ch1", ChannelType: 2, Payload: []byte("two")},
		},
		{
			Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
			Frame:   &frame.SendPacket{ClientSeq: 3, ClientMsgNo: "c", ChannelID: "ch1", ChannelType: 2, Payload: []byte("three")},
		},
	})
	if err != nil {
		t.Fatalf("OnSendBatch() error = %v", err)
	}
	first := requireSendack(t, written, 0)
	if first.ReasonCode != frame.ReasonNodeNotMatch {
		t.Fatalf("first ack reason = %v, want %v", first.ReasonCode, frame.ReasonNodeNotMatch)
	}
	second := requireSendack(t, written, 1)
	if second.ReasonCode != frame.ReasonNodeNotMatch {
		t.Fatalf("second ack reason = %v, want %v", second.ReasonCode, frame.ReasonNodeNotMatch)
	}
	third := requireSendack(t, written, 2)
	if third.ReasonCode != frame.ReasonChannelNotExist {
		t.Fatalf("third ack reason = %v, want %v", third.ReasonCode, frame.ReasonChannelNotExist)
	}
}

func TestOnSendBatchObservesSendackSourcesAndReasons(t *testing.T) {
	var written []frame.Frame
	sess := newTestSession(t, &written)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	observer := &recordingSendackObserver{}
	usecase := &recordingMessages{
		batchResults: []message.SendBatchItemResult{
			{Err: context.DeadlineExceeded},
			{Result: message.SendResult{MessageID: 11, MessageSeq: 101, Reason: message.ReasonSuccess}},
		},
	}
	handler := New(Options{Messages: usecase, SendTimeout: time.Second, SendackObserver: observer})

	err := handler.OnSendBatch([]coregateway.SendBatchItem{
		{
			Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
			Frame:   &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "a", ChannelID: "ch1", ChannelType: 2, Payload: []byte("one")},
		},
		{
			Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
			Frame:   &frame.SendPacket{ClientSeq: 2, ClientMsgNo: "b", ChannelID: "ch1", ChannelType: 2, Payload: []byte("two")},
		},
		{
			Context: coregateway.Context{Session: sess},
			Frame:   &frame.SendPacket{ClientSeq: 3, ClientMsgNo: "c", ChannelID: "ch1", ChannelType: 2, Payload: []byte("three")},
		},
	})
	if err != nil {
		t.Fatalf("OnSendBatch() error = %v", err)
	}
	if len(written) != 3 {
		t.Fatalf("written sendacks = %d, want 3", len(written))
	}
	want := []SendackEvent{
		{Reason: message.ReasonSystemError, Source: sendackSourceBatchResultError, ErrorClass: sendackErrorClassTimeout},
		{Reason: message.ReasonSuccess, Source: sendackSourceBatchResult, ErrorClass: sendackErrorClassNone},
		{Reason: message.ReasonSystemError, Source: sendackSourceBatchMissingRequestContext, ErrorClass: sendackErrorClassMissingRequestContext},
	}
	if len(observer.events) != len(want) {
		t.Fatalf("sendack events = %#v, want %#v", observer.events, want)
	}
	for i := range want {
		if observer.events[i] != want[i] {
			t.Fatalf("sendack event[%d] = %#v, want %#v", i, observer.events[i], want[i])
		}
	}
}

func TestOnSendBatchNilMessagesWritesSystemErrorSendacks(t *testing.T) {
	var written []frame.Frame
	var metas []session.OutboundMeta
	sess := newTestSessionWithMeta(t, &written, &metas)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	handler := New(Options{SendTimeout: time.Second})

	err := handler.OnSendBatch([]coregateway.SendBatchItem{
		{
			Context:    coregateway.Context{Session: sess, RequestContext: context.Background()},
			ReplyToken: "reply-1",
			Frame:      &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "a", ChannelID: "ch1", ChannelType: 2, Payload: []byte("one")},
		},
		{
			Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
			Frame:   &frame.SendPacket{ClientSeq: 2, ClientMsgNo: "b", ChannelID: "ch1", ChannelType: 2, Payload: []byte("two")},
		},
	})
	if err != nil {
		t.Fatalf("OnSendBatch() error = %v, want sendacks instead of raw error", err)
	}
	first := requireSendack(t, written, 0)
	if first.ClientSeq != 1 || first.ClientMsgNo != "a" || first.ReasonCode != frame.ReasonSystemError {
		t.Fatalf("first ack = %#v", first)
	}
	second := requireSendack(t, written, 1)
	if second.ClientSeq != 2 || second.ClientMsgNo != "b" || second.ReasonCode != frame.ReasonSystemError {
		t.Fatalf("second ack = %#v", second)
	}
	if got := metas[0].ReplyToken; got != "reply-1" {
		t.Fatalf("first reply token = %q, want reply-1", got)
	}
}

func TestOnSendBatchFallsBackToSingleSendUsecase(t *testing.T) {
	var written []frame.Frame
	var metas []session.OutboundMeta
	sess := newTestSessionWithMeta(t, &written, &metas)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	usecase := &singleOnlyMessages{
		results: []message.SendResult{
			{MessageID: 21, MessageSeq: 201, Reason: message.ReasonSuccess},
			{Reason: message.ReasonNodeNotMatch},
		},
	}
	handler := New(Options{Messages: usecase, SendTimeout: time.Second})

	err := handler.OnSendBatch([]coregateway.SendBatchItem{
		{
			Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
			Frame:   &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "a", ChannelID: "ch1", ChannelType: 2, Payload: []byte("one")},
		},
		{
			Context:    coregateway.Context{Session: sess, RequestContext: context.Background()},
			ReplyToken: "reply-2",
			Frame:      &frame.SendPacket{ClientSeq: 2, ClientMsgNo: "b", ChannelID: "ch1", ChannelType: 2, Payload: []byte("two")},
		},
	})
	if err != nil {
		t.Fatalf("OnSendBatch() error = %v", err)
	}
	if len(usecase.commands) != 2 {
		t.Fatalf("single send call count = %d, want 2", len(usecase.commands))
	}
	if usecase.commands[0].ClientMsgNo != "a" || usecase.commands[1].ClientMsgNo != "b" {
		t.Fatalf("single send commands not aligned: %#v", usecase.commands)
	}
	first := requireSendack(t, written, 0)
	if first.ClientSeq != 1 || first.ClientMsgNo != "a" || first.MessageID != 21 || first.MessageSeq != 201 || first.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("first ack = %#v", first)
	}
	second := requireSendack(t, written, 1)
	if second.ClientSeq != 2 || second.ClientMsgNo != "b" || second.ReasonCode != frame.ReasonNodeNotMatch {
		t.Fatalf("second ack = %#v", second)
	}
	if got := metas[1].ReplyToken; got != "reply-2" {
		t.Fatalf("second reply token = %q, want reply-2", got)
	}
}

func TestOnSendBatchReturnsErrorWhenBatchResultsHaveExtraItems(t *testing.T) {
	var written []frame.Frame
	sess := newTestSession(t, &written)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	usecase := &recordingMessages{
		batchResults: []message.SendBatchItemResult{
			{Result: message.SendResult{MessageID: 11, MessageSeq: 101, Reason: message.ReasonSuccess}},
			{Result: message.SendResult{MessageID: 12, MessageSeq: 102, Reason: message.ReasonSuccess}},
		},
	}
	handler := New(Options{Messages: usecase, SendTimeout: time.Second})

	err := handler.OnSendBatch([]coregateway.SendBatchItem{
		{
			Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
			Frame:   &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "a", ChannelID: "ch1", ChannelType: 2, Payload: []byte("one")},
		},
	})
	if err == nil {
		t.Fatalf("OnSendBatch() error = nil, want batch result count mismatch")
	}
	if len(written) != 0 {
		t.Fatalf("written frame count = %d, want 0 on mismatch", len(written))
	}
}

type recordingMessages struct {
	sendResult   message.SendResult
	sendErr      error
	sendCommands []message.SendCommand

	batchResults []message.SendBatchItemResult
	batchItems   []message.SendBatchItem
}

func (m *recordingMessages) Send(_ context.Context, cmd message.SendCommand) (message.SendResult, error) {
	m.sendCommands = append(m.sendCommands, cmd)
	return m.sendResult, m.sendErr
}

func (m *recordingMessages) SendBatch(items []message.SendBatchItem) []message.SendBatchItemResult {
	m.batchItems = append([]message.SendBatchItem(nil), items...)
	return m.batchResults
}

type singleOnlyMessages struct {
	results  []message.SendResult
	errs     []error
	commands []message.SendCommand
}

func (m *singleOnlyMessages) Send(_ context.Context, cmd message.SendCommand) (message.SendResult, error) {
	m.commands = append(m.commands, cmd)
	index := len(m.commands) - 1
	var result message.SendResult
	if index < len(m.results) {
		result = m.results[index]
	}
	var err error
	if index < len(m.errs) {
		err = m.errs[index]
	}
	return result, err
}

type recordingSendackObserver struct {
	events []SendackEvent
}

func (o *recordingSendackObserver) SendackWritten(event SendackEvent) {
	o.events = append(o.events, event)
}

type recordingDelivery struct {
	recvackErr      error
	closedErr       error
	recvackCommands []delivery.RecvackCommand
	closedCommands  []delivery.SessionClosedCommand
}

func (d *recordingDelivery) Recvack(_ context.Context, cmd delivery.RecvackCommand) error {
	d.recvackCommands = append(d.recvackCommands, cmd)
	return d.recvackErr
}

func (d *recordingDelivery) SessionClosed(_ context.Context, cmd delivery.SessionClosedCommand) error {
	d.closedCommands = append(d.closedCommands, cmd)
	return d.closedErr
}

type recordingPresence struct {
	activateErr        error
	deactivateErr      error
	touchErr           error
	activateContexts   []context.Context
	deactivateContexts []context.Context
	touchContexts      []context.Context
	activateCommands   []presence.ActivateCommand
	deactivateCommands []presence.DeactivateCommand
	touchCommands      []presence.TouchCommand
}

func (p *recordingPresence) Activate(ctx context.Context, cmd presence.ActivateCommand) error {
	p.activateContexts = append(p.activateContexts, ctx)
	p.activateCommands = append(p.activateCommands, cmd)
	return p.activateErr
}

func (p *recordingPresence) Deactivate(ctx context.Context, cmd presence.DeactivateCommand) error {
	p.deactivateContexts = append(p.deactivateContexts, ctx)
	p.deactivateCommands = append(p.deactivateCommands, cmd)
	return p.deactivateErr
}

func (p *recordingPresence) Touch(ctx context.Context, cmd presence.TouchCommand) error {
	p.touchContexts = append(p.touchContexts, ctx)
	p.touchCommands = append(p.touchCommands, cmd)
	return p.touchErr
}

type testContextKey struct{}

func newTestSession(t *testing.T, written *[]frame.Frame) session.Session {
	t.Helper()
	return newTestSessionWithMeta(t, written, nil)
}

func newTestSessionWithMeta(t *testing.T, written *[]frame.Frame, metas *[]session.OutboundMeta) session.Session {
	t.Helper()
	return session.New(session.Config{
		ID: 100,
		WriteFrameFn: func(f frame.Frame, meta session.OutboundMeta) error {
			*written = append(*written, f)
			if metas != nil {
				*metas = append(*metas, meta)
			}
			return nil
		},
	})
}

func requireSendack(t *testing.T, written []frame.Frame, index int) *frame.SendackPacket {
	t.Helper()
	if len(written) <= index {
		t.Fatalf("written frame count = %d, want index %d", len(written), index)
	}
	ack, ok := written[index].(*frame.SendackPacket)
	if !ok {
		t.Fatalf("written[%d] = %T, want *frame.SendackPacket", index, written[index])
	}
	return ack
}
