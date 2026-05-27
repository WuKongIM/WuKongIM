package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

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
	handler := New(Options{Messages: usecase, SendTimeout: time.Second})
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
	if cmd.FromUID != "u1" || cmd.SenderSessionID != sess.ID() || cmd.ProtocolVersion != 4 {
		t.Fatalf("mapped sender fields = uid=%q session=%d version=%d", cmd.FromUID, cmd.SenderSessionID, cmd.ProtocolVersion)
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

func TestOnFrameUnknownFrameReturnsErrUnsupportedFrame(t *testing.T) {
	handler := New(Options{Messages: &recordingMessages{}, SendTimeout: time.Second})

	err := handler.OnFrame(coregateway.Context{RequestContext: context.Background()}, &frame.PingPacket{})
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
	handler := New(Options{Messages: usecase, SendTimeout: time.Second})
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
