package gateway

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics/tracectx"
	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	gatewaysession "github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
	"github.com/stretchr/testify/require"
)

func TestHandlerOnSessionActivateCallsPresenceActivate(t *testing.T) {
	fixedNow := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	presenceUsecase := &fakePresenceUsecase{}
	handler := newHandlerWithPresence(t, presenceUsecase, Options{
		Now: func() time.Time { return fixedNow },
	})
	ctx := newAuthedContext(t, 1, "u1")
	ctx.Session.SetValue(coregateway.SessionValueDeviceID, "dev-1")

	activator, ok := any(handler).(interface {
		OnSessionActivate(*coregateway.Context) (*frame.ConnackPacket, error)
	})
	require.True(t, ok, "handler must implement session activation hook")

	connack, err := activator.OnSessionActivate(ctx)
	require.NoError(t, err)
	require.Nil(t, connack)
	require.Len(t, presenceUsecase.activateCommands, 1)
	cmd := presenceUsecase.activateCommands[0]
	require.Equal(t, "u1", cmd.UID)
	require.Equal(t, "dev-1", cmd.DeviceID)
	require.Equal(t, frame.APP, cmd.DeviceFlag)
	require.Equal(t, frame.DeviceLevelMaster, cmd.DeviceLevel)
	require.Equal(t, "tcp", cmd.Listener)
	require.Equal(t, fixedNow, cmd.ConnectedAt)
	require.NotNil(t, cmd.Session)
	require.Equal(t, ctx.Session.ID(), cmd.Session.ID())
	require.Equal(t, ctx.Session.Listener(), cmd.Session.Listener())
	cmd.Session.SetValue("adapter-key", "adapter-value")
	require.Equal(t, "adapter-value", ctx.Session.Value("adapter-key"))
}

func TestOnlineSessionAdapterImplementsRuntimeWriterAndForwards(t *testing.T) {
	var _ online.SessionWriter = onlineSessionAdapter{}
	var gotFrame frame.Frame
	var gotMeta gatewaysession.OutboundMeta
	sess := gatewaysession.New(gatewaysession.Config{
		ID:         42,
		Listener:   "tcp",
		RemoteAddr: "10.0.0.1:9000",
		LocalAddr:  "127.0.0.1:7000",
		WriteFrameFn: func(f frame.Frame, meta gatewaysession.OutboundMeta) error {
			gotFrame = f
			gotMeta = meta
			return nil
		},
	})
	sess.SetValue("device", "ios")

	writer := newOnlineSessionAdapter(sess)
	require.NotNil(t, writer)
	require.Equal(t, uint64(42), writer.ID())
	require.Equal(t, "tcp", writer.Listener())
	require.Equal(t, "10.0.0.1:9000", writer.RemoteAddr())
	require.Equal(t, "127.0.0.1:7000", writer.LocalAddr())
	require.Equal(t, "ios", writer.Value("device"))

	ping := &frame.PingPacket{}
	require.NoError(t, writer.WriteFrame(ping))
	require.Same(t, ping, gotFrame)
	require.Empty(t, gotMeta.ReplyToken)

	writer.SetValue("uid", "u1")
	require.Equal(t, "u1", sess.Value("uid"))
	require.NoError(t, writer.Close())
	require.ErrorIs(t, writer.WriteFrame(&frame.PingPacket{}), gatewaysession.ErrSessionClosed)
}

func TestHandlerOnSessionOpenIsNoop(t *testing.T) {
	handler := New(Options{Now: func() time.Time { return time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC) }})
	ctx := newAuthedContext(t, 1, "u1")

	require.NoError(t, handler.OnSessionOpen(*ctx))

	require.Empty(t, handler.online.ConnectionsByUID("u1"))
	_, ok := handler.online.Connection(1)
	require.False(t, ok)
}

func TestHandlerOnSessionCloseCallsPresenceDeactivate(t *testing.T) {
	msgs := &fakeMessageUsecase{}
	presenceUsecase := &fakePresenceUsecase{}
	handler := newHandlerWithPresence(t, presenceUsecase, Options{Messages: msgs})
	ctx := newAuthedContext(t, 1, "u1")

	require.NoError(t, handler.OnSessionClose(*ctx))
	require.Equal(t, []message.SessionClosedCommand{{
		UID:       "u1",
		SessionID: 1,
	}}, msgs.sessionClosed)
	require.Equal(t, []presence.DeactivateCommand{{
		UID:       "u1",
		SessionID: 1,
	}}, presenceUsecase.deactivateCommands)
}

func TestHandlerOnSessionActivateRejectsUnauthenticatedContext(t *testing.T) {
	handler := newHandlerWithPresence(t, &fakePresenceUsecase{}, Options{})

	activator, ok := any(handler).(interface {
		OnSessionActivate(*coregateway.Context) (*frame.ConnackPacket, error)
	})
	require.True(t, ok, "handler must implement session activation hook")

	_, err := activator.OnSessionActivate(&coregateway.Context{
		Session: gatewaysession.New(gatewaysession.Config{
			ID:       1,
			Listener: "tcp",
		}),
	})

	require.ErrorIs(t, err, ErrUnauthenticatedSession)
}

func TestHandlerOnSessionErrorDoesNotMutateRegistry(t *testing.T) {
	handler := New(Options{Now: func() time.Time { return time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC) }})
	ctx := newAuthedContext(t, 1, "u1")

	require.NoError(t, handler.OnSessionOpen(*ctx))
	before := handler.online.ConnectionsByUID("u1")

	handler.OnSessionError(*ctx, errors.New("boom"))

	after := handler.online.ConnectionsByUID("u1")
	require.Equal(t, before, after)
}

func TestHandlerOnFrameSendMapsCommandAndWritesSendack(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	handler := New(Options{
		Messages: &fakeMessageUsecase{
			sendResult: message.SendResult{
				MessageID:  99,
				MessageSeq: uint64(^uint32(0)) + 7,
				Reason:     frame.ReasonSuccess,
			},
		},
	})

	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		ReplyToken:     "reply-1",
		RequestContext: context.Background(),
	}
	pkt := &frame.SendPacket{
		Framer:      frame.Framer{RedDot: true, SyncOnce: true},
		Setting:     1,
		MsgKey:      "key-1",
		Expire:      10,
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Topic:       "chat",
		Payload:     []byte("hi"),
		ClientSeq:   13,
		ClientMsgNo: "m4",
		StreamNo:    "stream-1",
	}

	require.NoError(t, handler.OnFrame(*ctx, pkt))
	require.Len(t, sender.Writes(), 1)

	msgs := handler.messages.(*fakeMessageUsecase)
	require.Len(t, msgs.sendCommands, 1)
	require.Equal(t, "u1", msgs.sendCommands[0].FromUID)
	require.Equal(t, uint64(1), msgs.sendCommands[0].SenderSessionID)
	require.Equal(t, "u2", msgs.sendCommands[0].ChannelID)
	require.Equal(t, frame.ChannelTypePerson, msgs.sendCommands[0].ChannelType)
	require.Equal(t, uint64(13), msgs.sendCommands[0].ClientSeq)
	require.Equal(t, "m4", msgs.sendCommands[0].ClientMsgNo)
	require.Equal(t, "stream-1", msgs.sendCommands[0].StreamNo)
	require.Equal(t, "chat", msgs.sendCommands[0].Topic)
	require.Equal(t, []byte("hi"), msgs.sendCommands[0].Payload)
	require.True(t, msgs.sendCommands[0].Framer.RedDot)
	require.True(t, msgs.sendCommands[0].Framer.SyncOnce)

	write := sender.Writes()[0]
	ack := requireSendackPacket(t, write.f)
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
	require.Equal(t, int64(99), ack.MessageID)
	require.Equal(t, uint64(^uint32(0))+7, ack.MessageSeq)
	require.Equal(t, uint64(13), ack.ClientSeq)
	require.Equal(t, "m4", ack.ClientMsgNo)
	require.Equal(t, "reply-1", write.meta.ReplyToken)
}

func TestHandlerOnSendBatchWritesAlignedSendacks(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	msgs := &fakeMessageUsecase{
		sendBatchResults: []message.SendBatchItemResult{
			{Result: message.SendResult{MessageID: 101, MessageSeq: 11, Reason: frame.ReasonSuccess}},
			{Result: message.SendResult{MessageID: 102, MessageSeq: 12, Reason: frame.ReasonSuccess}},
		},
	}
	handler := New(Options{Messages: msgs})
	items := []coregateway.SendBatchItem{
		{
			Context: coregateway.Context{
				Session:        sender,
				Listener:       "tcp",
				ReplyToken:     "reply-1",
				RequestContext: context.Background(),
			},
			ReplyToken: "reply-1",
			Frame: &frame.SendPacket{
				ChannelID:   "u2",
				ChannelType: frame.ChannelTypePerson,
				ClientSeq:   21,
				ClientMsgNo: "batch-1",
				Payload:     []byte("one"),
			},
		},
		{
			Context: coregateway.Context{
				Session:        sender,
				Listener:       "tcp",
				ReplyToken:     "reply-2",
				RequestContext: context.Background(),
			},
			ReplyToken: "reply-2",
			Frame: &frame.SendPacket{
				ChannelID:   "u2",
				ChannelType: frame.ChannelTypePerson,
				ClientSeq:   22,
				ClientMsgNo: "batch-2",
				Payload:     []byte("two"),
			},
		},
	}

	require.NoError(t, handler.OnSendBatch(items))

	require.Len(t, msgs.sendBatchItems, 2)
	require.Equal(t, "batch-1", msgs.sendBatchItems[0].Command.ClientMsgNo)
	require.Equal(t, "batch-2", msgs.sendBatchItems[1].Command.ClientMsgNo)
	require.NotSame(t, msgs.sendBatchItems[0].Context, msgs.sendBatchItems[1].Context)
	writes := sender.Writes()
	require.Len(t, writes, 2)
	first := requireSendackPacket(t, writes[0].f)
	require.Equal(t, int64(101), first.MessageID)
	require.Equal(t, uint64(11), first.MessageSeq)
	require.Equal(t, uint64(21), first.ClientSeq)
	require.Equal(t, "batch-1", first.ClientMsgNo)
	require.Equal(t, "reply-1", writes[0].meta.ReplyToken)
	second := requireSendackPacket(t, writes[1].f)
	require.Equal(t, int64(102), second.MessageID)
	require.Equal(t, uint64(12), second.MessageSeq)
	require.Equal(t, uint64(22), second.ClientSeq)
	require.Equal(t, "batch-2", second.ClientMsgNo)
	require.Equal(t, "reply-2", writes[1].meta.ReplyToken)
}

func TestHandlerOnSendBatchRejectsInvalidItemWithoutBlockingValidItem(t *testing.T) {
	invalidSender := newOptionRecordingSession(2, "tcp")
	sender := newOptionRecordingSession(1, "tcp")
	msgs := &fakeMessageUsecase{
		sendBatchResults: []message.SendBatchItemResult{
			{Result: message.SendResult{MessageID: 201, MessageSeq: 31, Reason: frame.ReasonSuccess}},
		},
	}
	handler := New(Options{Messages: msgs})
	validCtx := coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		ReplyToken:     "reply-valid",
		RequestContext: context.Background(),
	}
	validCtx.Session.SetValue(coregateway.SessionValueUID, "u1")

	err := handler.OnSendBatch([]coregateway.SendBatchItem{
		{
			Context: coregateway.Context{
				Session:        invalidSender,
				Listener:       "tcp",
				ReplyToken:     "reply-invalid",
				RequestContext: context.Background(),
			},
			ReplyToken: "reply-invalid",
			Frame: &frame.SendPacket{
				ChannelID:   "u2",
				ChannelType: frame.ChannelTypePerson,
				ClientSeq:   30,
				ClientMsgNo: "invalid",
			},
		},
		{
			Context:    validCtx,
			ReplyToken: "reply-valid",
			Frame: &frame.SendPacket{
				ChannelID:   "u2",
				ChannelType: frame.ChannelTypePerson,
				ClientSeq:   31,
				ClientMsgNo: "valid",
			},
		},
	})

	require.NoError(t, err)
	require.Len(t, msgs.sendBatchItems, 1)
	require.Equal(t, "valid", msgs.sendBatchItems[0].Command.ClientMsgNo)
	invalidWrites := invalidSender.Writes()
	require.Len(t, invalidWrites, 1)
	rejected := requireSendackPacket(t, invalidWrites[0].f)
	require.Equal(t, frame.ReasonAuthFail, rejected.ReasonCode)
	require.Equal(t, "reply-invalid", invalidWrites[0].meta.ReplyToken)
	writes := sender.Writes()
	require.Len(t, writes, 1)
	accepted := requireSendackPacket(t, writes[0].f)
	require.Equal(t, frame.ReasonSuccess, accepted.ReasonCode)
	require.Equal(t, int64(201), accepted.MessageID)
	require.Equal(t, uint64(31), accepted.MessageSeq)
	require.Equal(t, "reply-valid", writes[0].meta.ReplyToken)
}

func TestHandlerOnFrameSendPreservesBusinessDenialReason(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	handler := New(Options{
		Messages: &fakeMessageUsecase{
			sendResult: message.SendResult{Reason: frame.ReasonInBlacklist},
		},
	})

	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		RequestContext: context.Background(),
	}
	pkt := &frame.SendPacket{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
		ClientSeq:   14,
		ClientMsgNo: "denied-1",
	}

	require.NoError(t, handler.OnFrame(*ctx, pkt))
	require.Len(t, sender.Writes(), 1)
	ack := requireSendackPacket(t, sender.Writes()[0].f)
	require.Equal(t, frame.ReasonInBlacklist, ack.ReasonCode)
	require.Zero(t, ack.MessageID)
	require.Zero(t, ack.MessageSeq)
	require.Equal(t, uint64(14), ack.ClientSeq)
	require.Equal(t, "denied-1", ack.ClientMsgNo)
}

func TestHandlerOnFrameSendWritesRateLimitReason(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	handler := New(Options{
		Messages: &fakeMessageUsecase{
			sendResult: message.SendResult{Reason: frame.ReasonRateLimit},
		},
	})

	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		RequestContext: context.Background(),
	}
	err := handler.OnFrame(*ctx, &frame.SendPacket{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hot"),
		ClientSeq:   15,
		ClientMsgNo: "limited-1",
	})

	require.NoError(t, err)
	ack := requireSendackPacket(t, sender.Writes()[0].f)
	require.Equal(t, frame.ReasonRateLimit, ack.ReasonCode)
	require.Equal(t, uint64(15), ack.ClientSeq)
	require.Equal(t, "limited-1", ack.ClientMsgNo)
}

func TestHandlerOnFrameSendDecryptsEncryptedPayloadBeforeUsecase(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	setEncryptedSession(t, sender, wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	})
	msgs := &fakeMessageUsecase{
		sendResult: message.SendResult{
			MessageID:  42,
			MessageSeq: 9,
			Reason:     frame.ReasonSuccess,
		},
	}
	handler := New(Options{Messages: msgs})

	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		ReplyToken:     "reply-encrypted",
		RequestContext: context.Background(),
	}
	packet := &frame.SendPacket{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
		ClientSeq:   5,
		ClientMsgNo: "m-encrypted",
	}
	mustEncryptSendPacket(t, packet, wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	})

	require.NoError(t, handler.OnFrame(*ctx, packet))
	require.Len(t, msgs.sendCommands, 1)
	require.Equal(t, []byte("hi"), msgs.sendCommands[0].Payload)
	require.Equal(t, "", msgs.sendCommands[0].MsgKey)
}

func TestHandlerOnFrameSendRejectsInvalidEncryptedMsgKey(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	setEncryptedSession(t, sender, wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	})
	msgs := &fakeMessageUsecase{}
	handler := New(Options{Messages: msgs})

	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		ReplyToken:     "reply-msg-key",
		RequestContext: context.Background(),
	}
	packet := &frame.SendPacket{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
		ClientSeq:   6,
		ClientMsgNo: "m-msg-key",
	}
	mustEncryptSendPacket(t, packet, wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	})
	packet.MsgKey = "bad-key"

	require.NoError(t, handler.OnFrame(*ctx, packet))
	require.Empty(t, msgs.sendCommands)
	require.Len(t, sender.Writes(), 1)
	ack := requireSendackPacket(t, sender.Writes()[0].f)
	require.Equal(t, frame.ReasonMsgKeyError, ack.ReasonCode)
}

func TestHandlerOnFrameSendRejectsUndecryptableEncryptedPayload(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	setEncryptedSession(t, sender, wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	})
	msgs := &fakeMessageUsecase{}
	handler := New(Options{Messages: msgs})

	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		ReplyToken:     "reply-payload",
		RequestContext: context.Background(),
	}
	packet := &frame.SendPacket{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
		ClientSeq:   7,
		ClientMsgNo: "m-payload",
	}
	mustEncryptSendPacket(t, packet, wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	})
	packet.Payload = []byte("not-base64")
	msgKey, err := wkprotoenc.SendMsgKey(packet, wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	})
	require.NoError(t, err)
	packet.MsgKey = msgKey

	require.NoError(t, handler.OnFrame(*ctx, packet))
	require.Empty(t, msgs.sendCommands)
	require.Len(t, sender.Writes(), 1)
	ack := requireSendackPacket(t, sender.Writes()[0].f)
	require.Equal(t, frame.ReasonPayloadDecodeError, ack.ReasonCode)
}

func TestHandlerOnFrameSendBypassesEncryptedSessionWhenPacketDisablesEncryption(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	setEncryptedSession(t, sender, wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	})
	msgs := &fakeMessageUsecase{
		sendResult: message.SendResult{Reason: frame.ReasonSuccess},
	}
	handler := New(Options{Messages: msgs})

	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		ReplyToken:     "reply-no-encrypt",
		RequestContext: context.Background(),
	}
	packet := &frame.SendPacket{
		Setting:     frame.SettingNoEncrypt,
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("plain"),
		ClientSeq:   8,
		ClientMsgNo: "m-no-encrypt",
	}

	require.NoError(t, handler.OnFrame(*ctx, packet))
	require.Len(t, msgs.sendCommands, 1)
	require.Equal(t, []byte("plain"), msgs.sendCommands[0].Payload)
}

func TestHandlerOnFrameSendMapsDeviceIdentityToUsecase(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	sender.SetValue(coregateway.SessionValueDeviceID, "system-device")
	sender.SetValue(coregateway.SessionValueDeviceFlag, frame.SYSTEM)
	msgs := &fakeMessageUsecase{
		sendResult: message.SendResult{Reason: frame.ReasonSuccess},
	}
	handler := New(Options{Messages: msgs})

	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		RequestContext: context.Background(),
	}
	require.NoError(t, handler.OnFrame(*ctx, &frame.SendPacket{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
	}))

	require.Len(t, msgs.sendCommands, 1)
	require.Equal(t, "system-device", msgs.sendCommands[0].DeviceID)
	require.Equal(t, frame.DeviceFlag(frame.SYSTEM), msgs.sendCommands[0].DeviceFlag)
}

func TestHandlerOnFrameSendPassesPrecomposedPersonChannelToUsecase(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	msgs := &fakeMessageUsecase{
		sendResult: message.SendResult{
			MessageID:  77,
			MessageSeq: 9,
			Reason:     frame.ReasonSuccess,
		},
	}
	handler := New(Options{Messages: msgs})

	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		ReplyToken:     "reply-precomposed",
		RequestContext: context.Background(),
	}

	require.NoError(t, handler.OnFrame(*ctx, &frame.SendPacket{
		ChannelID:   "u1@u2",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   1,
		ClientMsgNo: "m-pre",
	}))

	require.Len(t, msgs.sendCommands, 1)
	require.Equal(t, "u1@u2", msgs.sendCommands[0].ChannelID)
}

func TestHandlerOnFrameSendMapsUsecaseInvalidPersonChannelError(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	msgs := &fakeMessageUsecase{sendErr: runtimechannelid.ErrInvalidPersonChannel}
	handler := New(Options{Messages: msgs})

	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		ReplyToken:     "reply-invalid-channel",
		RequestContext: context.Background(),
	}

	require.NoError(t, handler.OnFrame(*ctx, &frame.SendPacket{
		ChannelID:   "u3@u4",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   2,
		ClientMsgNo: "m-invalid",
	}))

	require.Len(t, msgs.sendCommands, 1)
	require.Equal(t, "u3@u4", msgs.sendCommands[0].ChannelID)
	require.Len(t, sender.Writes(), 1)
	ack := requireSendackPacket(t, sender.Writes()[0].f)
	require.Equal(t, frame.ReasonChannelIDError, ack.ReasonCode)
	require.Equal(t, uint64(2), ack.ClientSeq)
	require.Equal(t, "m-invalid", ack.ClientMsgNo)
}

func TestHandlerOnFrameSendPropagatesRequestContext(t *testing.T) {
	type ctxKey string

	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	msgs := &fakeMessageUsecase{}
	handler := New(Options{Messages: msgs})

	reqCtx := context.WithValue(context.Background(), ctxKey("request"), "gateway-send")
	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		ReplyToken:     "reply-ctx",
		RequestContext: reqCtx,
	}
	pkt := &frame.SendPacket{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   33,
		ClientMsgNo: "ctx-1",
	}

	require.NoError(t, handler.OnFrame(*ctx, pkt))
	require.Len(t, msgs.sendContexts, 1)
	require.Equal(t, "gateway-send", msgs.sendContexts[0].Value(ctxKey("request")))
	_, ok := msgs.sendContexts[0].Deadline()
	require.True(t, ok)
}

func TestHandleSendAssignsTraceIDToCommandAndSendTrace(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	msgs := &fakeMessageUsecase{
		sendResult: message.SendResult{MessageID: 99, MessageSeq: 7, Reason: frame.ReasonSuccess},
	}
	handler := New(Options{Messages: msgs})
	sink := &recordingSendTraceSink{}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)

	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		ReplyToken:     "reply-trace",
		RequestContext: context.Background(),
	}
	pkt := &frame.SendPacket{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   36,
		ClientMsgNo: "trace-msg",
		Payload:     []byte("hi"),
	}

	require.NoError(t, handler.handleSend(ctx, pkt))

	require.Len(t, msgs.sendCommands, 1)
	traceID := msgs.sendCommands[0].TraceID
	require.Len(t, traceID, 32)
	events := sink.snapshot()
	require.NotEmpty(t, events)
	var sendEvent sendtrace.Event
	var ackEvent sendtrace.Event
	for _, event := range events {
		if event.Stage == sendtrace.StageGatewayMessagesSend {
			sendEvent = event
		}
		if event.Stage == sendtrace.StageGatewayWriteSendack {
			ackEvent = event
		}
	}
	require.Equal(t, traceID, sendEvent.TraceID)
	wantKey := string(channelhandler.KeyFromChannelID(channel.ChannelID{ID: "u2", Type: frame.ChannelTypePerson}))
	require.Equal(t, wantKey, sendEvent.ChannelKey)
	require.Equal(t, "u1", sendEvent.FromUID)
	require.Equal(t, traceID, ackEvent.TraceID)
	require.Equal(t, wantKey, ackEvent.ChannelKey)
	require.Equal(t, "u1", ackEvent.FromUID)
}

func TestHandleSendSkipsTraceIDWhenSendTraceDisabled(t *testing.T) {
	restore := sendtrace.SetSink(nil)
	t.Cleanup(restore)

	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	msgs := &fakeMessageUsecase{
		sendResult: message.SendResult{MessageID: 99, MessageSeq: 7, Reason: frame.ReasonSuccess},
	}
	handler := New(Options{Messages: msgs})

	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		ReplyToken:     "reply-trace-disabled",
		RequestContext: context.Background(),
	}
	pkt := &frame.SendPacket{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   37,
		ClientMsgNo: "trace-disabled-msg",
		Payload:     []byte("hi"),
	}

	require.NoError(t, handler.handleSend(ctx, pkt))

	require.Len(t, msgs.sendCommands, 1)
	require.Empty(t, msgs.sendCommands[0].TraceID)
	require.Len(t, msgs.sendContexts, 1)
	_, ok := tracectx.FromContext(msgs.sendContexts[0])
	require.False(t, ok)
}

func TestHandlerOnFrameSendMapsCanceledRequestContextToSendack(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	msgs := &fakeMessageUsecase{
		sendFn: func(ctx context.Context, _ message.SendCommand) (message.SendResult, error) {
			return message.SendResult{}, ctx.Err()
		},
	}
	handler := New(Options{Messages: msgs})

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		ReplyToken:     "reply-canceled",
		RequestContext: reqCtx,
	}

	err := handler.OnFrame(*ctx, &frame.SendPacket{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   34,
		ClientMsgNo: "ctx-canceled",
	})

	require.NoError(t, err)
	require.Len(t, sender.Writes(), 1)
	ack := requireSendackPacket(t, sender.Writes()[0].f)
	require.Equal(t, frame.ReasonSystemError, ack.ReasonCode)
	require.Equal(t, uint64(34), ack.ClientSeq)
	require.Equal(t, "ctx-canceled", ack.ClientMsgNo)
	require.Len(t, msgs.sendContexts, 1)
	require.ErrorIs(t, msgs.sendContexts[0].Err(), context.Canceled)
}

func TestHandlerOnFrameSendMapsSendTimeoutToSendack(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	msgs := &fakeMessageUsecase{
		sendFn: func(ctx context.Context, _ message.SendCommand) (message.SendResult, error) {
			<-ctx.Done()
			return message.SendResult{}, ctx.Err()
		},
	}
	handler := New(Options{
		Messages:    msgs,
		SendTimeout: 5 * time.Millisecond,
	})
	ctx := &coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		ReplyToken:     "reply-timeout",
		RequestContext: context.Background(),
	}

	err := handler.OnFrame(*ctx, &frame.SendPacket{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   35,
		ClientMsgNo: "ctx-timeout",
	})

	require.NoError(t, err)
	require.Len(t, sender.Writes(), 1)
	ack := requireSendackPacket(t, sender.Writes()[0].f)
	require.Equal(t, frame.ReasonSystemError, ack.ReasonCode)
	require.Equal(t, uint64(35), ack.ClientSeq)
	require.Equal(t, "ctx-timeout", ack.ClientMsgNo)
	require.Len(t, msgs.sendContexts, 1)
	require.ErrorIs(t, msgs.sendContexts[0].Err(), context.DeadlineExceeded)
}

func TestHandlerOnFrameSendMapsChannelclusterErrorsToSendack(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		reason frame.ReasonCode
	}{
		{
			name:   "channel deleting",
			err:    channel.ErrChannelDeleting,
			reason: frame.ReasonChannelDeleting,
		},
		{
			name:   "protocol upgrade required",
			err:    channel.ErrProtocolUpgradeRequired,
			reason: frame.ReasonProtocolUpgradeRequired,
		},
		{
			name:   "idempotency conflict",
			err:    channel.ErrIdempotencyConflict,
			reason: frame.ReasonIdempotencyConflict,
		},
		{
			name:   "message seq exhausted",
			err:    channel.ErrMessageSeqExhausted,
			reason: frame.ReasonMessageSeqExhausted,
		},
		{
			name:   "stale meta",
			err:    channel.ErrStaleMeta,
			reason: frame.ReasonNodeNotMatch,
		},
		{
			name:   "not leader",
			err:    channel.ErrNotLeader,
			reason: frame.ReasonNodeNotMatch,
		},
		{
			name:   "invalid agent channel",
			err:    runtimechannelid.ErrInvalidAgentChannel,
			reason: frame.ReasonChannelIDError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := newOptionRecordingSession(1, "tcp")
			sender.SetValue(coregateway.SessionValueUID, "u1")
			handler := New(Options{
				Messages: &fakeMessageUsecase{sendErr: tt.err},
			})

			ctx := &coregateway.Context{
				Session:        sender,
				Listener:       "tcp",
				ReplyToken:     "reply-2",
				RequestContext: context.Background(),
			}
			pkt := &frame.SendPacket{
				ChannelID:   "u2",
				ChannelType: frame.ChannelTypePerson,
				ClientSeq:   13,
				ClientMsgNo: "m4",
			}

			require.NoError(t, handler.OnFrame(*ctx, pkt))
			require.Len(t, sender.Writes(), 1)

			ack := requireSendackPacket(t, sender.Writes()[0].f)
			require.Equal(t, tt.reason, ack.ReasonCode)
			require.Zero(t, ack.MessageID)
			require.Zero(t, ack.MessageSeq)
			require.Equal(t, uint64(13), ack.ClientSeq)
			require.Equal(t, "m4", ack.ClientMsgNo)
		})
	}
}

func TestHandlerOnFrameRecvackRoutesToMessageUsecase(t *testing.T) {
	msgs := &fakeMessageUsecase{}
	handler := New(Options{Messages: msgs})

	err := handler.OnFrame(*newAuthedContext(t, 1, "u1"), &frame.RecvackPacket{
		Framer:     frame.Framer{RedDot: true},
		MessageID:  88,
		MessageSeq: 9,
	})

	require.NoError(t, err)
	require.Equal(t, 1, msgs.recvAckCalls)
	require.Len(t, msgs.recvAckCommands, 1)
	require.Equal(t, "u1", msgs.recvAckCommands[0].UID)
	require.Equal(t, uint64(1), msgs.recvAckCommands[0].SessionID)
	require.Equal(t, int64(88), msgs.recvAckCommands[0].MessageID)
	require.Equal(t, uint64(9), msgs.recvAckCommands[0].MessageSeq)
	require.True(t, msgs.recvAckCommands[0].Framer.RedDot)
}

func TestHandlerOnFramePingWritesPong(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	handler := New(Options{Messages: &fakeMessageUsecase{}})

	err := handler.OnFrame(coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		RequestContext: context.Background(),
	}, &frame.PingPacket{})

	require.NoError(t, err)
	require.Len(t, sender.Writes(), 1)
	_, ok := sender.Writes()[0].f.(*frame.PongPacket)
	require.True(t, ok, "expected *frame.PongPacket, got %T", sender.Writes()[0].f)
}

func TestNewSharesOnlineRegistryWithInjectedMessageApp(t *testing.T) {
	msgApp := newClusterBackedMessageApp(channel.AppendResult{
		MessageID:  88,
		MessageSeq: 9,
	})
	handler := New(Options{
		Messages: msgApp,
		Now:      func() time.Time { return fixedGatewayNow },
	})

	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	sender.SetValue(coregateway.SessionValueDeviceFlag, frame.APP)
	sender.SetValue(coregateway.SessionValueDeviceLevel, frame.DeviceLevelMaster)

	recipient := newOptionRecordingSession(2, "tcp")
	recipient.SetValue(coregateway.SessionValueUID, "u2")
	recipient.SetValue(coregateway.SessionValueDeviceFlag, frame.APP)
	recipient.SetValue(coregateway.SessionValueDeviceLevel, frame.DeviceLevelMaster)

	require.NoError(t, msgApp.OnlineRegistry().Register(online.OnlineConn{
		SessionID:   sender.ID(),
		UID:         "u1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Listener:    "tcp",
		ConnectedAt: fixedGatewayNow,
		Session:     newOnlineSessionAdapter(sender),
	}))
	require.NoError(t, msgApp.OnlineRegistry().Register(online.OnlineConn{
		SessionID:   recipient.ID(),
		UID:         "u2",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Listener:    "tcp",
		ConnectedAt: fixedGatewayNow,
		Session:     newOnlineSessionAdapter(recipient),
	}))
	require.Same(t, msgApp.OnlineRegistry(), handler.online)
	require.Len(t, handler.online.ConnectionsByUID("u2"), 1)

	err := handler.OnFrame(coregateway.Context{
		Session:        sender,
		Listener:       "tcp",
		RequestContext: context.Background(),
	}, &frame.SendPacket{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
		ClientSeq:   1,
		ClientMsgNo: "m1",
	})
	require.NoError(t, err)

	require.Len(t, sender.Writes(), 1)
	ack := requireSendackPacket(t, sender.Writes()[0].f)
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
	require.Empty(t, recipient.Writes())
}

func newAuthedContext(t *testing.T, sessionID uint64, uid string) *coregateway.Context {
	t.Helper()

	sess := gatewaysession.New(gatewaysession.Config{
		ID:       sessionID,
		Listener: "tcp",
	})
	sess.SetValue(coregateway.SessionValueUID, uid)
	sess.SetValue(coregateway.SessionValueDeviceFlag, frame.APP)
	sess.SetValue(coregateway.SessionValueDeviceLevel, frame.DeviceLevelMaster)

	return &coregateway.Context{
		Session:        sess,
		Listener:       "tcp",
		RequestContext: context.Background(),
	}
}

func newHandlerWithPresence(t *testing.T, presenceUsecase *fakePresenceUsecase, opts Options) *Handler {
	t.Helper()

	optionsValue := reflect.ValueOf(&opts).Elem()
	presenceField := optionsValue.FieldByName("Presence")
	if !presenceField.IsValid() {
		t.Fatalf("gateway.Options is missing Presence field")
	}
	require.True(t, presenceField.CanSet())
	presenceField.Set(reflect.ValueOf(presenceUsecase))
	return New(opts)
}

func requireSendackPacket(t *testing.T, f frame.Frame) *frame.SendackPacket {
	t.Helper()

	ack, ok := f.(*frame.SendackPacket)
	require.True(t, ok, "expected *frame.SendackPacket, got %T", f)
	return ack
}

func setEncryptedSession(t *testing.T, sess gatewaysession.Session, keys wkprotoenc.SessionKeys) {
	t.Helper()

	sessionCrypto, err := wkprotoenc.NewSessionCrypto(keys)
	require.NoError(t, err)
	sess.SetValue(coregateway.SessionValueEncryptionEnabled, true)
	sess.SetValue(coregateway.SessionValueAESKey, append([]byte(nil), keys.AESKey...))
	sess.SetValue(coregateway.SessionValueAESIV, append([]byte(nil), keys.AESIV...))
	sess.SetValue(coregateway.SessionValueCrypto, sessionCrypto)
}

func mustEncryptSendPacket(t *testing.T, packet *frame.SendPacket, keys wkprotoenc.SessionKeys) {
	t.Helper()

	encrypted, err := wkprotoenc.EncryptPayload(packet.Payload, keys)
	require.NoError(t, err)
	packet.Payload = encrypted
	packet.MsgKey, err = wkprotoenc.SendMsgKey(packet, keys)
	require.NoError(t, err)
}

type fakeMessageUsecase struct {
	sendCommands     []message.SendCommand
	sendContexts     []context.Context
	sendFn           func(context.Context, message.SendCommand) (message.SendResult, error)
	sendResult       message.SendResult
	sendErr          error
	sendBatchItems   []message.SendBatchItem
	sendBatchResults []message.SendBatchItemResult
	sessionClosed    []message.SessionClosedCommand
	sessionCloseErr  error
	recvAckCalls     int
	recvAckCommands  []message.RecvAckCommand
	recvAckErr       error
}

func (f *fakeMessageUsecase) Send(ctx context.Context, cmd message.SendCommand) (message.SendResult, error) {
	f.sendContexts = append(f.sendContexts, ctx)
	f.sendCommands = append(f.sendCommands, cmd)
	if f.sendFn != nil {
		return f.sendFn(ctx, cmd)
	}
	return f.sendResult, f.sendErr
}

func (f *fakeMessageUsecase) SendBatch(items []message.SendBatchItem) []message.SendBatchItemResult {
	f.sendBatchItems = append(f.sendBatchItems, items...)
	if f.sendBatchResults != nil {
		return append([]message.SendBatchItemResult(nil), f.sendBatchResults...)
	}
	results := make([]message.SendBatchItemResult, len(items))
	for i, item := range items {
		result, err := f.Send(item.Context, item.Command)
		results[i] = message.SendBatchItemResult{Result: result, Err: err}
	}
	return results
}

func (f *fakeMessageUsecase) RecvAck(cmd message.RecvAckCommand) error {
	f.recvAckCalls++
	f.recvAckCommands = append(f.recvAckCommands, cmd)
	return f.recvAckErr
}

func (f *fakeMessageUsecase) SessionClosed(cmd message.SessionClosedCommand) error {
	f.sessionClosed = append(f.sessionClosed, cmd)
	return f.sessionCloseErr
}

type fakePresenceUsecase struct {
	activateCommands   []presence.ActivateCommand
	activateErr        error
	deactivateCommands []presence.DeactivateCommand
	deactivateErr      error
}

func (f *fakePresenceUsecase) Activate(_ context.Context, cmd presence.ActivateCommand) error {
	f.activateCommands = append(f.activateCommands, cmd)
	return f.activateErr
}

func (f *fakePresenceUsecase) Deactivate(_ context.Context, cmd presence.DeactivateCommand) error {
	f.deactivateCommands = append(f.deactivateCommands, cmd)
	return f.deactivateErr
}

type outboundWrite struct {
	f    frame.Frame
	meta gatewaysession.OutboundMeta
}

type optionRecordingSession struct {
	gatewaysession.Session
	mu     sync.Mutex
	writes []outboundWrite
}

func newOptionRecordingSession(id uint64, listener string) *optionRecordingSession {
	recorder := &optionRecordingSession{}
	recorder.Session = gatewaysession.New(gatewaysession.Config{
		ID:       id,
		Listener: listener,
		WriteFrameFn: func(f frame.Frame, meta gatewaysession.OutboundMeta) error {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			recorder.writes = append(recorder.writes, outboundWrite{f: f, meta: meta})
			return nil
		},
	})
	return recorder
}

func (s *optionRecordingSession) Writes() []outboundWrite {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]outboundWrite, len(s.writes))
	copy(out, s.writes)
	return out
}

var fixedGatewayNow = time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

type fakeIdentityStore struct{}

func (*fakeIdentityStore) GetUser(context.Context, string) (metadb.User, error) {
	return metadb.User{}, nil
}

type fakeChannelStore struct{}

func (*fakeChannelStore) GetChannel(context.Context, string, int64) (metadb.Channel, error) {
	return metadb.Channel{}, nil
}

type fakeChannelAppender struct {
	result channel.AppendResult
	err    error
}

func (f *fakeChannelAppender) Append(context.Context, channel.AppendRequest) (channel.AppendResult, error) {
	return f.result, f.err
}

func (f *fakeChannelAppender) AppendBatch(_ context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	if f.err != nil {
		return channel.AppendBatchResult{}, f.err
	}
	items := make([]channel.AppendBatchItemResult, len(req.Messages))
	for i := range req.Messages {
		items[i] = channel.AppendBatchItemResult{
			MessageID:  f.result.MessageID,
			MessageSeq: f.result.MessageSeq,
			Message:    f.result.Message,
		}
	}
	return channel.AppendBatchResult{Items: items}, nil
}

func newClusterBackedMessageApp(result channel.AppendResult) *message.App {
	return newClusterBackedMessageAppWithOnline(nil, result)
}

func newClusterBackedMessageAppWithOnline(registry online.Registry, result channel.AppendResult) *message.App {
	return message.New(message.Options{
		IdentityStore:   &fakeIdentityStore{},
		ChannelStore:    &fakeChannelStore{},
		ChannelAppender: &fakeChannelAppender{result: result},
		Online:          registry,
		Now:             func() time.Time { return fixedGatewayNow },
	})
}

var _ online.Registry = (*online.MemoryRegistry)(nil)

type recordingSendTraceSink struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (s *recordingSendTraceSink) RecordSendTrace(event sendtrace.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingSendTraceSink) snapshot() []sendtrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sendtrace.Event, len(s.events))
	copy(out, s.events)
	return out
}
