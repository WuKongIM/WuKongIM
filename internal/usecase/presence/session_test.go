package presence

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type presenceTestSession struct {
	id       uint64
	listener string

	values sync.Map

	mu     sync.Mutex
	frames []frame.Frame
	closed bool
}

func newPresenceTestSession(id uint64, listener string) *presenceTestSession {
	return &presenceTestSession{id: id, listener: listener}
}

func (s *presenceTestSession) ID() uint64 {
	return s.id
}

func (s *presenceTestSession) Listener() string {
	return s.listener
}

func (s *presenceTestSession) RemoteAddr() string {
	return ""
}

func (s *presenceTestSession) LocalAddr() string {
	return ""
}

func (s *presenceTestSession) WriteFrame(f frame.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = append(s.frames, f)
	return nil
}

func (s *presenceTestSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *presenceTestSession) SetValue(key string, value any) {
	s.values.Store(key, value)
}

func (s *presenceTestSession) Value(key string) any {
	value, _ := s.values.Load(key)
	return value
}

var _ online.SessionWriter = (*presenceTestSession)(nil)
