package session

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var (
	ErrSessionClosed    = errors.New("gateway/session: session is closed")
	ErrWriteQueueFull   = errors.New("gateway/session: write queue is full")
	ErrOutboundOverflow = errors.New("gateway/session: outbound bytes limit exceeded")
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

type EncodedQueue interface {
	EnqueueEncoded(payload []byte) error
	DequeueEncoded() ([]byte, bool)
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
	ID               uint64
	Listener         string
	RemoteAddr       string
	LocalAddr        string
	WriteQueueSize   int
	MaxOutboundBytes int64
	WriteFrameFn     WriteFrameFn
}

func New(cfg Config) Session {
	return newSession(
		cfg.ID,
		cfg.Listener,
		cfg.RemoteAddr,
		cfg.LocalAddr,
		cfg.WriteQueueSize,
		cfg.MaxOutboundBytes,
		cfg.WriteFrameFn,
	)
}

type session struct {
	id         uint64
	listener   string
	remoteAddr string
	localAddr  string

	values sync.Map

	writeMu          sync.Mutex
	closing          atomic.Bool
	closed           atomic.Bool
	queueMu          sync.Mutex
	writeCh          chan []byte
	outboundBytes    int64
	maxOutboundBytes int64
	writeFrameFn     WriteFrameFn
}

func newSession(id uint64, listener, remoteAddr, localAddr string, writeQueueSize int, maxOutboundBytes int64, writeFrameFn WriteFrameFn) *session {
	if writeQueueSize <= 0 {
		writeQueueSize = 1
	}
	if maxOutboundBytes < 0 {
		maxOutboundBytes = 0
	}
	return &session{
		id:               id,
		listener:         listener,
		remoteAddr:       remoteAddr,
		localAddr:        localAddr,
		writeCh:          make(chan []byte, writeQueueSize),
		maxOutboundBytes: maxOutboundBytes,
		writeFrameFn:     writeFrameFn,
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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closing.Load() || s.closed.Load() {
		return ErrSessionClosed
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
	s.queueMu.Lock()
	close(s.writeCh)
	s.queueMu.Unlock()
	return nil
}

func (s *session) SetValue(key string, value any) {
	if s == nil {
		return
	}
	s.values.Store(key, value)
}

func (s *session) Value(key string) any {
	if s == nil {
		return nil
	}
	value, _ := s.values.Load(key)
	return value
}

func (s *session) enqueueEncoded(payload []byte) error {
	if s == nil {
		return ErrSessionClosed
	}
	queued := append([]byte(nil), payload...)
	size := int64(len(queued))

	if s.closed.Load() {
		return ErrSessionClosed
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()

	if s.closed.Load() {
		return ErrSessionClosed
	}
	if s.maxOutboundBytes > 0 && s.outboundBytes+size > s.maxOutboundBytes {
		return ErrOutboundOverflow
	}

	select {
	case s.writeCh <- queued:
		s.outboundBytes += size
		return nil
	default:
		return ErrWriteQueueFull
	}
}

func (s *session) dequeueEncoded() ([]byte, bool) {
	if s == nil {
		return nil, false
	}

	payload, ok := <-s.writeCh
	if !ok {
		return nil, false
	}

	s.queueMu.Lock()
	s.outboundBytes -= int64(len(payload))
	s.queueMu.Unlock()

	return payload, true
}

func (s *session) EnqueueEncoded(payload []byte) error {
	return s.enqueueEncoded(payload)
}

func (s *session) DequeueEncoded() ([]byte, bool) {
	return s.dequeueEncoded()
}
