package gateway

import (
	coregateway "github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/wkprotoenc"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func decryptSendPacketIfNeeded(ctx *coregateway.Context, pkt *frame.SendPacket) (frame.ReasonCode, error) {
	if pkt == nil || ctx == nil || ctx.Session == nil {
		return frame.ReasonSuccess, nil
	}
	if pkt.Setting.IsSet(frame.SettingNoEncrypt) || !wkprotoenc.SessionEncryptionEnabled(ctx.Session) {
		return frame.ReasonSuccess, nil
	}

	plain, err := decryptSendPacketPayload(ctx, pkt)
	if err != nil {
		if err == wkprotoenc.ErrMsgKeyMismatch {
			return frame.ReasonMsgKeyError, err
		}
		return frame.ReasonPayloadDecodeError, err
	}
	pkt.Payload = plain
	pkt.MsgKey = ""
	return frame.ReasonSuccess, nil
}

func decryptSendPacketPayload(ctx *coregateway.Context, pkt *frame.SendPacket) ([]byte, error) {
	if sessionCrypto, ok := wkprotoenc.SessionCryptoFromSession(ctx.Session); ok {
		if err := wkprotoenc.ValidateSendPacketWithCrypto(pkt, sessionCrypto); err != nil {
			return nil, err
		}
		return wkprotoenc.DecryptPayloadWithCrypto(pkt.Payload, sessionCrypto)
	}
	keys, ok := wkprotoenc.SessionKeysFromSession(ctx.Session)
	if !ok {
		return nil, wkprotoenc.ErrMissingSessionKey
	}
	if err := wkprotoenc.ValidateSendPacket(pkt, keys); err != nil {
		return nil, err
	}
	return wkprotoenc.DecryptPayload(pkt.Payload, keys)
}
