package wkproto

import (
	"github.com/WuKongIM/WuKongIM/pkg/gateway/protocol"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/wkprotoenc"
	codec "github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const Name = "wkproto"

type Adapter struct {
	codec *codec.WKProto
}

var _ protocol.DecodedFrameOwner = (*Adapter)(nil)

func New() *Adapter {
	return &Adapter{
		codec: codec.New(),
	}
}

func (a *Adapter) Name() string {
	if a == nil {
		return ""
	}
	return Name
}

// OwnsDecodedFrames reports that WKProto Decode detaches payload bytes used by async dispatch.
func (a *Adapter) OwnsDecodedFrames() bool {
	return a != nil
}

func (a *Adapter) Decode(sess session.Session, in []byte) ([]frame.Frame, int, error) {
	if a == nil || len(in) == 0 {
		return nil, 0, nil
	}

	frames := make([]frame.Frame, 0, 1)
	consumed := 0
	version := uint8(frame.LatestVersion)
	if sessVersion, ok := sessionVersion(sess, false); ok {
		version = sessVersion
	}
	for consumed < len(in) {
		f, n, err := a.codec.DecodeFrame(in[consumed:], version)
		if err != nil {
			return nil, 0, err
		}
		if f == nil || n == 0 {
			break
		}
		if send, ok := f.(*frame.SendPacket); ok {
			if !send.Setting.IsSet(frame.SettingNoEncrypt) && wkprotoenc.SessionEncryptionEnabled(sess) {
				if err := decryptSendPacketForSession(sess, send); err != nil {
					return nil, 0, err
				}
			}
			detachSendPayload(send)
		}
		frames = append(frames, f)
		consumed += n
	}

	return frames, consumed, nil
}

func detachSendPayload(send *frame.SendPacket) {
	if send == nil || len(send.Payload) == 0 {
		return
	}
	send.Payload = append([]byte(nil), send.Payload...)
}

func (a *Adapter) Encode(sess session.Session, f frame.Frame, _ session.OutboundMeta) ([]byte, error) {
	if a == nil {
		return nil, nil
	}
	if recv, ok := f.(*frame.RecvPacket); ok && !recv.Setting.IsSet(frame.SettingNoEncrypt) && wkprotoenc.SessionEncryptionEnabled(sess) {
		sealed, err := sealRecvPacketForSession(sess, recv)
		if err != nil {
			return nil, err
		}
		f = sealed
	}
	version, ok := sessionVersion(sess, true)
	if !ok {
		version = uint8(frame.LegacyMessageSeqVersion)
	}
	return a.codec.EncodeFrame(f, version)
}

func sealRecvPacketForSession(sess session.Session, recv *frame.RecvPacket) (*frame.RecvPacket, error) {
	if sessionCrypto, ok := wkprotoenc.SessionCryptoFromSession(sess); ok {
		return wkprotoenc.SealRecvPacketWithCrypto(recv, sessionCrypto)
	}
	keys, ok := wkprotoenc.SessionKeysFromSession(sess)
	if !ok {
		return nil, wkprotoenc.ErrMissingSessionKey
	}
	return wkprotoenc.SealRecvPacket(recv, keys)
}

func decryptSendPacketForSession(sess session.Session, send *frame.SendPacket) error {
	if sessionCrypto, ok := wkprotoenc.SessionCryptoFromSession(sess); ok {
		if err := wkprotoenc.ValidateSendPacketWithCrypto(send, sessionCrypto); err != nil {
			return err
		}
		plain, err := wkprotoenc.DecryptPayloadWithCrypto(send.Payload, sessionCrypto)
		if err != nil {
			return err
		}
		send.Payload = plain
		return nil
	}
	keys, ok := wkprotoenc.SessionKeysFromSession(sess)
	if !ok {
		return wkprotoenc.ErrMissingSessionKey
	}
	if err := wkprotoenc.ValidateSendPacket(send, keys); err != nil {
		return err
	}
	plain, err := wkprotoenc.DecryptPayload(send.Payload, keys)
	if err != nil {
		return err
	}
	send.Payload = plain
	return nil
}

func (a *Adapter) OnOpen(session.Session) error {
	return nil
}

func (a *Adapter) OnClose(session.Session) error {
	return nil
}

func sessionVersion(sess session.Session, outbound bool) (uint8, bool) {
	if sess == nil {
		if outbound {
			return 0, false
		}
		return frame.LatestVersion, true
	}
	value := sess.Value(gatewaytypes.SessionValueProtocolVersion)
	version, ok := value.(uint8)
	if !ok || version == 0 {
		if outbound {
			return 0, false
		}
		return frame.LatestVersion, true
	}
	return version, true
}
