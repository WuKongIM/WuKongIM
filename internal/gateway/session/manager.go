package session

import "sync"

type Manager struct {
	mu       sync.RWMutex
	sessions map[uint64]Session
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[uint64]Session),
	}
}

func (m *Manager) Add(sess Session) {
	if m == nil || sess == nil {
		return
	}

	m.mu.Lock()
	m.sessions[sess.ID()] = sess
	m.mu.Unlock()
}

func (m *Manager) Get(id uint64) (Session, bool) {
	if m == nil {
		return nil, false
	}

	m.mu.RLock()
	sess, ok := m.sessions[id]
	m.mu.RUnlock()
	return sess, ok
}

func (m *Manager) Remove(id uint64) {
	if m == nil {
		return
	}

	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

func (m *Manager) Range(fn func(Session) bool) {
	if m == nil || fn == nil {
		return
	}

	m.mu.RLock()
	sessions := make([]Session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		sessions = append(sessions, sess)
	}
	m.mu.RUnlock()

	for _, sess := range sessions {
		if !fn(sess) {
			return
		}
	}
}

func (m *Manager) Count() int {
	if m == nil {
		return 0
	}

	m.mu.RLock()
	count := len(m.sessions)
	m.mu.RUnlock()
	return count
}
