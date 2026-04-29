package delivery

import "sync"

type ackKey struct {
	sessionID uint64
	messageID uint64
}

type AckIndex struct {
	mu      sync.RWMutex
	entries map[ackKey]AckBinding
	reverse map[uint64]map[ackKey]struct{}
}

func NewAckIndex() *AckIndex {
	return &AckIndex{
		entries: make(map[ackKey]AckBinding),
		reverse: make(map[uint64]map[ackKey]struct{}),
	}
}

func (i *AckIndex) Bind(binding AckBinding) {
	if i == nil {
		return
	}
	key := ackKey{sessionID: binding.SessionID, messageID: binding.MessageID}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.entries[key] = binding
	sessionBindings := i.reverse[binding.SessionID]
	if sessionBindings == nil {
		sessionBindings = make(map[ackKey]struct{})
		i.reverse[binding.SessionID] = sessionBindings
	}
	sessionBindings[key] = struct{}{}
}

func (i *AckIndex) refresh(binding AckBinding) bool {
	if i == nil {
		return false
	}
	key := ackKey{sessionID: binding.SessionID, messageID: binding.MessageID}
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, ok := i.entries[key]; !ok {
		return false
	}
	i.entries[key] = binding
	sessionBindings := i.reverse[binding.SessionID]
	if sessionBindings == nil {
		sessionBindings = make(map[ackKey]struct{})
		i.reverse[binding.SessionID] = sessionBindings
	}
	sessionBindings[key] = struct{}{}
	return true
}

func (i *AckIndex) Lookup(sessionID, messageID uint64) (AckBinding, bool) {
	if i == nil {
		return AckBinding{}, false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	binding, ok := i.entries[ackKey{sessionID: sessionID, messageID: messageID}]
	return binding, ok
}

// Take atomically returns and removes an acknowledgement binding.
func (i *AckIndex) Take(sessionID, messageID uint64) (AckBinding, bool) {
	if i == nil {
		return AckBinding{}, false
	}
	key := ackKey{sessionID: sessionID, messageID: messageID}
	i.mu.Lock()
	defer i.mu.Unlock()
	binding, ok := i.entries[key]
	if !ok {
		return AckBinding{}, false
	}
	i.removeLocked(key)
	return binding, true
}

// TakeSession atomically returns and removes all acknowledgement bindings for a session.
func (i *AckIndex) TakeSession(sessionID uint64) []AckBinding {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	keys := i.reverse[sessionID]
	if len(keys) == 0 {
		return nil
	}
	out := make([]AckBinding, 0, len(keys))
	for key := range keys {
		if binding, ok := i.entries[key]; ok {
			out = append(out, binding)
			delete(i.entries, key)
		}
	}
	delete(i.reverse, sessionID)
	return out
}

func (i *AckIndex) LookupSession(sessionID uint64) []AckBinding {
	if i == nil {
		return nil
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	keys := i.reverse[sessionID]
	if len(keys) == 0 {
		return nil
	}
	out := make([]AckBinding, 0, len(keys))
	for key := range keys {
		out = append(out, i.entries[key])
	}
	return out
}

// Len returns the current number of acknowledgement bindings.
func (i *AckIndex) Len() int {
	if i == nil {
		return 0
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.entries)
}

func (i *AckIndex) Remove(sessionID, messageID uint64) {
	if i == nil {
		return
	}
	key := ackKey{sessionID: sessionID, messageID: messageID}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.removeLocked(key)
}

func (i *AckIndex) removeLocked(key ackKey) {
	binding, ok := i.entries[key]
	if !ok {
		return
	}
	delete(i.entries, key)
	if sessionBindings := i.reverse[binding.SessionID]; sessionBindings != nil {
		delete(sessionBindings, key)
		if len(sessionBindings) == 0 {
			delete(i.reverse, binding.SessionID)
		}
	}
}
