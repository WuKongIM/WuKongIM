package session

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var (
	ErrSessionClosed = errors.New("gateway/session: session is closed")
	// ErrOutboundSealed reports a write attempted after the terminal frame was
	// admitted. A sealed session never reopens, including when that admission
	// returned an error.
	ErrOutboundSealed = errors.New("gateway/session: outbound is sealed")
	// ErrOutboundSealUnsupported reports a Session implementation that does not
	// provide the optional atomic terminal-write capability.
	ErrOutboundSealUnsupported = errors.New("gateway/session: outbound seal unsupported")
)

type Session interface {
	ID() uint64
	Listener() string
	RemoteAddr() string
	LocalAddr() string

	WriteFrame(f frame.Frame, opts ...WriteOption) error
	Close() error

	SetValue(key string, value any)
	Value(key string) any
}

// OutboundSealer atomically closes ordinary outbound admission and admits one
// final frame through the same ordering lock used by WriteFrame.
type OutboundSealer interface {
	SealOutboundAndWrite(f frame.Frame, opts ...WriteOption) error
}

// OutboundSealState exposes only the terminal business-admission gate.
type OutboundSealState interface {
	OutboundSealed() bool
}

type WriteOption interface {
	apply(*OutboundMeta)
}

type WriteFrameFn func(f frame.Frame, meta OutboundMeta) error

type OutboundMeta struct {
	ReplyToken string
}

type replyTokenOption string

func (o replyTokenOption) apply(meta *OutboundMeta) {
	meta.ReplyToken = string(o)
}

func WithReplyToken(token string) WriteOption {
	return replyTokenOption(token)
}

type Config struct {
	ID           uint64
	Listener     string
	RemoteAddr   string
	LocalAddr    string
	WriteFrameFn WriteFrameFn
}

func New(cfg Config) Session {
	return newSession(
		cfg.ID,
		cfg.Listener,
		cfg.RemoteAddr,
		cfg.LocalAddr,
		cfg.WriteFrameFn,
	)
}

type session struct {
	id         uint64
	listener   string
	remoteAddr string
	localAddr  string

	hotValues atomic.Pointer[sessionHotValues]
	values    sync.Map

	writeMu        sync.Mutex
	outboundSealed atomic.Bool
	closing        atomic.Bool
	closed         atomic.Bool
	writeFrameFn   WriteFrameFn
}

// These keys mirror gateway/types session value keys without importing that package.
const (
	hotSessionValueUID               = "gateway.uid"
	hotSessionValueDeviceID          = "gateway.device_id"
	hotSessionValueDeviceFlag        = "gateway.device_flag"
	hotSessionValueDeviceLevel       = "gateway.device_level"
	hotSessionValueProtocolVersion   = "gateway.protocol_version"
	hotSessionValueProtocolName      = "gateway.protocol_name"
	hotSessionValueEncryptionEnabled = "gateway.encryption_enabled"
	hotSessionValueAESKey            = "gateway.aes_key"
	hotSessionValueAESIV             = "gateway.aes_iv"
	hotSessionValueCrypto            = "gateway.wkproto_crypto"
)

type sessionHotValues struct {
	uid               any
	deviceID          any
	deviceFlag        any
	deviceLevel       any
	protocolVersion   any
	protocolName      any
	encryptionEnabled any
	aesKey            any
	aesIV             any
	crypto            any

	uidSet               bool
	deviceIDSet          bool
	deviceFlagSet        bool
	deviceLevelSet       bool
	protocolVersionSet   bool
	protocolNameSet      bool
	encryptionEnabledSet bool
	aesKeySet            bool
	aesIVSet             bool
	cryptoSet            bool
}

func newSession(id uint64, listener, remoteAddr, localAddr string, writeFrameFn WriteFrameFn) *session {
	return &session{
		id:           id,
		listener:     listener,
		remoteAddr:   remoteAddr,
		localAddr:    localAddr,
		writeFrameFn: writeFrameFn,
	}
}

func (s *session) ID() uint64 {
	if s == nil {
		return 0
	}
	return s.id
}

func (s *session) Listener() string {
	if s == nil {
		return ""
	}
	return s.listener
}

func (s *session) RemoteAddr() string {
	if s == nil {
		return ""
	}
	return s.remoteAddr
}

func (s *session) LocalAddr() string {
	if s == nil {
		return ""
	}
	return s.localAddr
}

func (s *session) WriteFrame(f frame.Frame, opts ...WriteOption) error {
	if s == nil {
		return ErrSessionClosed
	}
	if s.closing.Load() || s.closed.Load() {
		return ErrSessionClosed
	}
	if s.outboundSealed.Load() {
		return ErrOutboundSealed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closing.Load() || s.closed.Load() {
		return ErrSessionClosed
	}
	if s.outboundSealed.Load() {
		return ErrOutboundSealed
	}

	meta := OutboundMeta{}
	for _, opt := range opts {
		if opt != nil {
			opt.apply(&meta)
		}
	}
	if s.writeFrameFn == nil {
		return nil
	}
	return s.writeFrameFn(f, meta)
}

// SealOutboundAndWrite is the final ordered session write. It marks the
// session sealed before invoking the transport enqueue callback, so callback
// failure cannot accidentally reopen ordinary outbound admission.
func (s *session) SealOutboundAndWrite(f frame.Frame, opts ...WriteOption) error {
	if s == nil {
		return ErrSessionClosed
	}
	if s.closing.Load() || s.closed.Load() {
		return ErrSessionClosed
	}
	if s.outboundSealed.Load() {
		return ErrOutboundSealed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closing.Load() || s.closed.Load() {
		return ErrSessionClosed
	}
	if s.outboundSealed.Load() {
		return ErrOutboundSealed
	}
	s.outboundSealed.Store(true)

	meta := OutboundMeta{}
	for _, opt := range opts {
		if opt != nil {
			opt.apply(&meta)
		}
	}
	if s.writeFrameFn == nil {
		return nil
	}
	return s.writeFrameFn(f, meta)
}

// OutboundSealed reports the terminal admission state. Entry adapters use it
// to reject every later ordinary inbound frame before invoking any use case;
// relying on a later response write failure would allow post-fence work.
func (s *session) OutboundSealed() bool {
	return s != nil && s.outboundSealed.Load()
}

func (s *session) Close() error {
	if s == nil {
		return nil
	}
	s.closing.Store(true)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed.Load() {
		return nil
	}
	s.closed.Store(true)
	return nil
}

func (s *session) SetValue(key string, value any) {
	if s == nil {
		return
	}
	if s.setHotValue(key, value) {
		return
	}
	s.values.Store(key, value)
}

func (s *session) Value(key string) any {
	if s == nil {
		return nil
	}
	if value, ok := s.hotValue(key); ok {
		return value
	}
	value, _ := s.values.Load(key)
	return value
}

// LoadOrStoreValue atomically initializes extension state without widening the Session interface.
func (s *session) LoadOrStoreValue(key string, value any) (actual any, loaded bool) {
	if s == nil {
		return nil, false
	}
	if isHotValueKey(key) {
		for {
			current := s.hotValues.Load()
			if actual, loaded := current.load(key); loaded {
				return actual, true
			}

			next := sessionHotValues{}
			if current != nil {
				next = *current
			}
			next.set(key, value)
			if s.hotValues.CompareAndSwap(current, &next) {
				return value, false
			}
		}
	}
	return s.values.LoadOrStore(key, value)
}

func (s *session) setHotValue(key string, value any) bool {
	if !isHotValueKey(key) {
		return false
	}

	for {
		current := s.hotValues.Load()
		next := sessionHotValues{}
		if current != nil {
			next = *current
		}
		next.set(key, value)
		if s.hotValues.CompareAndSwap(current, &next) {
			return true
		}
	}
}

func (s *session) hotValue(key string) (any, bool) {
	if !isHotValueKey(key) {
		return nil, false
	}
	values := s.hotValues.Load()
	if values == nil {
		return nil, true
	}
	value, _ := values.load(key)
	return value, true
}

func isHotValueKey(key string) bool {
	switch key {
	case hotSessionValueUID,
		hotSessionValueDeviceID,
		hotSessionValueDeviceFlag,
		hotSessionValueDeviceLevel,
		hotSessionValueProtocolVersion,
		hotSessionValueProtocolName,
		hotSessionValueEncryptionEnabled,
		hotSessionValueAESKey,
		hotSessionValueAESIV,
		hotSessionValueCrypto:
		return true
	default:
		return false
	}
}

func (v *sessionHotValues) set(key string, value any) {
	switch key {
	case hotSessionValueUID:
		v.uid, v.uidSet = value, true
	case hotSessionValueDeviceID:
		v.deviceID, v.deviceIDSet = value, true
	case hotSessionValueDeviceFlag:
		v.deviceFlag, v.deviceFlagSet = value, true
	case hotSessionValueDeviceLevel:
		v.deviceLevel, v.deviceLevelSet = value, true
	case hotSessionValueProtocolVersion:
		v.protocolVersion, v.protocolVersionSet = value, true
	case hotSessionValueProtocolName:
		v.protocolName, v.protocolNameSet = value, true
	case hotSessionValueEncryptionEnabled:
		v.encryptionEnabled, v.encryptionEnabledSet = value, true
	case hotSessionValueAESKey:
		v.aesKey, v.aesKeySet = value, true
	case hotSessionValueAESIV:
		v.aesIV, v.aesIVSet = value, true
	case hotSessionValueCrypto:
		v.crypto, v.cryptoSet = value, true
	}
}

func (v *sessionHotValues) load(key string) (any, bool) {
	if v == nil {
		return nil, false
	}
	switch key {
	case hotSessionValueUID:
		if v.uidSet {
			return v.uid, true
		}
	case hotSessionValueDeviceID:
		if v.deviceIDSet {
			return v.deviceID, true
		}
	case hotSessionValueDeviceFlag:
		if v.deviceFlagSet {
			return v.deviceFlag, true
		}
	case hotSessionValueDeviceLevel:
		if v.deviceLevelSet {
			return v.deviceLevel, true
		}
	case hotSessionValueProtocolVersion:
		if v.protocolVersionSet {
			return v.protocolVersion, true
		}
	case hotSessionValueProtocolName:
		if v.protocolNameSet {
			return v.protocolName, true
		}
	case hotSessionValueEncryptionEnabled:
		if v.encryptionEnabledSet {
			return v.encryptionEnabled, true
		}
	case hotSessionValueAESKey:
		if v.aesKeySet {
			return v.aesKey, true
		}
	case hotSessionValueAESIV:
		if v.aesIVSet {
			return v.aesIV, true
		}
	case hotSessionValueCrypto:
		if v.cryptoSet {
			return v.crypto, true
		}
	}
	return nil, false
}
