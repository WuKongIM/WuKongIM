package delivery

import "sync"

type ackKey struct {
	uid       string
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
	key := ackKeyFromBinding(binding)
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
	key := ackKeyFromBinding(binding)
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
	return i.lookupLocked("", sessionID, messageID, false)
}

// LookupRoute returns the binding for one UID-scoped session/message pair.
func (i *AckIndex) LookupRoute(uid string, sessionID, messageID uint64) (AckBinding, bool) {
	if i == nil {
		return AckBinding{}, false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.lookupLocked(uid, sessionID, messageID, true)
}

// Take atomically returns and removes an acknowledgement binding.
func (i *AckIndex) Take(sessionID, messageID uint64) (AckBinding, bool) {
	if i == nil {
		return AckBinding{}, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	key, binding, ok := i.lookupKeyLocked("", sessionID, messageID, false)
	if !ok {
		return AckBinding{}, false
	}
	i.removeLocked(key)
	return binding, true
}

// TakeRoute atomically returns and removes one UID-scoped route binding.
func (i *AckIndex) TakeRoute(uid string, sessionID, messageID uint64) (AckBinding, bool) {
	if i == nil {
		return AckBinding{}, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	key, binding, ok := i.lookupKeyLocked(uid, sessionID, messageID, true)
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

// TakeSessionRoute atomically returns and removes all bindings for one UID-scoped session.
func (i *AckIndex) TakeSessionRoute(uid string, sessionID uint64) []AckBinding {
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
		if key.uid != uid {
			continue
		}
		if binding, ok := i.entries[key]; ok {
			out = append(out, binding)
			i.removeLocked(key)
		}
	}
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
		if binding, ok := i.entries[key]; ok {
			out = append(out, binding)
		}
	}
	return out
}

// LookupSessionRoute returns all bindings for one UID-scoped session.
func (i *AckIndex) LookupSessionRoute(uid string, sessionID uint64) []AckBinding {
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
		if key.uid != uid {
			continue
		}
		if binding, ok := i.entries[key]; ok {
			out = append(out, binding)
		}
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
	i.mu.Lock()
	defer i.mu.Unlock()
	key, _, ok := i.lookupKeyLocked("", sessionID, messageID, false)
	if !ok {
		return
	}
	i.removeLocked(key)
}

// RemoveRoute removes one UID-scoped route binding.
func (i *AckIndex) RemoveRoute(uid string, sessionID, messageID uint64) {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	key, _, ok := i.lookupKeyLocked(uid, sessionID, messageID, true)
	if !ok {
		return
	}
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

func ackKeyFromBinding(binding AckBinding) ackKey {
	return ackKey{
		uid:       binding.Route.UID,
		sessionID: binding.SessionID,
		messageID: binding.MessageID,
	}
}

func (i *AckIndex) lookupLocked(uid string, sessionID, messageID uint64, exactUID bool) (AckBinding, bool) {
	_, binding, ok := i.lookupKeyLocked(uid, sessionID, messageID, exactUID)
	return binding, ok
}

func (i *AckIndex) lookupKeyLocked(uid string, sessionID, messageID uint64, exactUID bool) (ackKey, AckBinding, bool) {
	if exactUID {
		key := ackKey{uid: uid, sessionID: sessionID, messageID: messageID}
		binding, ok := i.entries[key]
		return key, binding, ok
	}
	keys := i.reverse[sessionID]
	for key := range keys {
		if key.messageID != messageID {
			continue
		}
		binding, ok := i.entries[key]
		return key, binding, ok
	}
	return ackKey{}, AckBinding{}, false
}
