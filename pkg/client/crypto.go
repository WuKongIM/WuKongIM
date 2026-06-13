package client

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

type cryptoState struct {
	private [32]byte
	public  [32]byte
	keys    wkprotoenc.SessionKeys
	session *wkprotoenc.SessionCrypto
}

func newCryptoState() (*cryptoState, error) {
	private, public, err := wkprotoenc.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	return &cryptoState{private: private, public: public}, nil
}

func (s *cryptoState) connectPacket(opts ConnectOptions) *frame.ConnectPacket {
	return &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		ClientKey:       wkprotoenc.EncodePublicKey(s.public),
		DeviceID:        opts.DeviceID,
		DeviceFlag:      opts.DeviceFlag,
		ClientTimestamp: time.Now().UnixMilli(),
		UID:             opts.UID,
		Token:           opts.Token,
	}
}

func (s *cryptoState) applyConnack(ack *frame.ConnackPacket) error {
	if ack.ServerKey == "" || ack.Salt == "" {
		s.keys = wkprotoenc.SessionKeys{}
		s.session = nil
		return nil
	}
	keys, err := wkprotoenc.DeriveClientSession(s.private, ack.ServerKey, ack.Salt)
	if err != nil {
		return err
	}
	session, err := wkprotoenc.NewSessionCrypto(keys)
	if err != nil {
		return err
	}
	s.keys = keys
	s.session = session
	return nil
}
