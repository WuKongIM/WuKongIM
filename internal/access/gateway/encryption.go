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

	keys, ok := wkprotoenc.SessionKeysFromSession(ctx.Session)
	if !ok {
		return frame.ReasonPayloadDecodeError, wkprotoenc.ErrMissingSessionKey
	}
	if err := wkprotoenc.ValidateSendPacket(pkt, keys); err != nil {
		if err == wkprotoenc.ErrMsgKeyMismatch {
			return frame.ReasonMsgKeyError, err
		}
		return frame.ReasonPayloadDecodeError, err
	}

	plain, err := wkprotoenc.DecryptPayload(pkt.Payload, keys)
	if err != nil {
		return frame.ReasonPayloadDecodeError, err
	}
	pkt.Payload = plain
	pkt.MsgKey = ""
	return frame.ReasonSuccess, nil
}
