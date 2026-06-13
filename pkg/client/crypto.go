package client

import (
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

type cryptoState struct {
	mu      sync.RWMutex
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
	s.mu.Lock()
	defer s.mu.Unlock()

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

func (s *cryptoState) currentSession() *wkprotoenc.SessionCrypto {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.session
}

func buildSendPacket(msg Message, assignedSeq uint64) (*frame.SendPacket, error) {
	if len(msg.Payload) > codec.PayloadMaxSize {
		return nil, ErrPayloadTooLarge
	}
	if msg.ChannelID == "" {
		return nil, fmt.Errorf("%w: channel id is required", ErrInvalidMessage)
	}
	if msg.ChannelType == 0 {
		return nil, fmt.Errorf("%w: channel type is required", ErrInvalidMessage)
	}
	if assignedSeq == 0 {
		return nil, fmt.Errorf("%w: client sequence is required", ErrInvalidMessage)
	}
	if assignedSeq > uint64(^uint32(0)) {
		return nil, ErrClientSeqExhausted
	}

	return &frame.SendPacket{
		Setting:     msg.Setting,
		Expire:      msg.Expire,
		ClientSeq:   assignedSeq,
		ClientMsgNo: msg.ClientMsgNo,
		ChannelID:   msg.ChannelID,
		ChannelType: msg.ChannelType,
		Topic:       msg.Topic,
		Payload:     append([]byte(nil), msg.Payload...),
	}, nil
}

func (s *cryptoState) sealSend(pkt *frame.SendPacket) (*frame.SendPacket, error) {
	if pkt == nil || pkt.Setting.IsSet(frame.SettingNoEncrypt) || s == nil {
		return pkt, nil
	}

	s.mu.RLock()
	session := s.session
	if session == nil {
		s.mu.RUnlock()
		return pkt, nil
	}
	sealed := *pkt
	encrypted, err := wkprotoenc.EncryptPayloadWithCrypto(pkt.Payload, session)
	if err != nil {
		s.mu.RUnlock()
		return nil, err
	}
	sealed.Payload = encrypted
	sealed.MsgKey, err = wkprotoenc.SendMsgKeyWithCrypto(&sealed, session)
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return &sealed, nil
}

func sealSendWithSession(pkt *frame.SendPacket, session *wkprotoenc.SessionCrypto) (*frame.SendPacket, error) {
	if pkt == nil || pkt.Setting.IsSet(frame.SettingNoEncrypt) || session == nil {
		return pkt, nil
	}
	sealed := *pkt
	encrypted, err := wkprotoenc.EncryptPayloadWithCrypto(pkt.Payload, session)
	if err != nil {
		return nil, err
	}
	sealed.Payload = encrypted
	sealed.MsgKey, err = wkprotoenc.SendMsgKeyWithCrypto(&sealed, session)
	if err != nil {
		return nil, err
	}
	return &sealed, nil
}
