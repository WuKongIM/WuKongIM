package testkit

import (
	"github.com/WuKongIM/WuKongIM/internal/gateway/wkprotoenc"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type WKProtoClient struct {
	private [32]byte
	public  [32]byte
	keys    wkprotoenc.SessionKeys
}

func NewWKProtoClient() (*WKProtoClient, error) {
	private, public, err := wkprotoenc.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	client := &WKProtoClient{private: private}
	client.public = public
	return client, nil
}

func (c *WKProtoClient) UseClientKey(packet *frame.ConnectPacket) (*frame.ConnectPacket, error) {
	if c == nil || packet == nil {
		return packet, nil
	}
	cloned := *packet
	cloned.ClientKey = wkprotoenc.EncodePublicKey(c.public)
	return &cloned, nil
}

func (c *WKProtoClient) ApplyConnack(connack *frame.ConnackPacket) error {
	if c == nil || connack == nil || connack.ServerKey == "" || connack.Salt == "" {
		return nil
	}
	keys, err := wkprotoenc.DeriveClientSession(c.private, connack.ServerKey, connack.Salt)
	if err != nil {
		return err
	}
	c.keys = keys
	return nil
}

func (c *WKProtoClient) EncryptSendPacket(packet *frame.SendPacket) error {
	if c == nil || packet == nil || packet.Setting.IsSet(frame.SettingNoEncrypt) {
		return nil
	}
	encrypted, err := wkprotoenc.EncryptPayload(packet.Payload, c.keys)
	if err != nil {
		return err
	}
	packet.Payload = encrypted
	packet.MsgKey, err = wkprotoenc.SendMsgKey(packet, c.keys)
	return err
}

func (c *WKProtoClient) DecryptRecvPacket(packet *frame.RecvPacket) error {
	if c == nil || packet == nil || packet.Setting.IsSet(frame.SettingNoEncrypt) {
		return nil
	}
	plain, err := wkprotoenc.DecryptPayload(packet.Payload, c.keys)
	if err != nil {
		return err
	}
	packet.Payload = plain
	return nil
}

func (c *WKProtoClient) Keys() wkprotoenc.SessionKeys {
	if c == nil {
		return wkprotoenc.SessionKeys{}
	}
	return wkprotoenc.SessionKeys{
		AESKey: append([]byte(nil), c.keys.AESKey...),
		AESIV:  append([]byte(nil), c.keys.AESIV...),
	}
}
