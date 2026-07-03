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
	reverse map[uint64]sessionAckKeys
}

// sessionAckKeys keeps the common one-outstanding-ack case inline.
type sessionAckKeys struct {
	// single is the only reverse key while singleSet is true.
	single ackKey
	// singleSet reports whether single contains an active key.
	singleSet bool
	// many is allocated only after one session has multiple outstanding acks.
	many map[ackKey]struct{}
}

func NewAckIndex() *AckIndex {
	return &AckIndex{
		entries: make(map[ackKey]AckBinding),
		reverse: make(map[uint64]sessionAckKeys),
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
	i.addReverseLocked(binding.SessionID, key)
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
	i.addReverseLocked(binding.SessionID, key)
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
	if keys.len() == 0 {
		return nil
	}
	out := make([]AckBinding, 0, keys.len())
	for _, key := range keys.list(nil) {
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
	if keys.len() == 0 {
		return nil
	}
	out := make([]AckBinding, 0, keys.len())
	for _, key := range keys.list(nil) {
		if key.uid != uid {
			continue
		}
		if binding, ok := i.entries[key]; ok {
			out = append(out, binding)
			delete(i.entries, key)
			keys.remove(key)
		}
	}
	if keys.len() == 0 {
		delete(i.reverse, sessionID)
	} else {
		i.reverse[sessionID] = keys
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
	if keys.len() == 0 {
		return nil
	}
	out := make([]AckBinding, 0, keys.len())
	for _, key := range keys.list(nil) {
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
	if keys.len() == 0 {
		return nil
	}
	out := make([]AckBinding, 0, keys.len())
	for _, key := range keys.list(nil) {
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
	if sessionBindings, ok := i.reverse[binding.SessionID]; ok {
		sessionBindings.remove(key)
		if sessionBindings.len() == 0 {
			delete(i.reverse, binding.SessionID)
			return
		}
		i.reverse[binding.SessionID] = sessionBindings
	}
}

func (i *AckIndex) addReverseLocked(sessionID uint64, key ackKey) {
	sessionBindings := i.reverse[sessionID]
	sessionBindings.add(key)
	i.reverse[sessionID] = sessionBindings
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
	key, ok := keys.findMessage(messageID)
	if ok {
		binding, ok := i.entries[key]
		return key, binding, ok
	}
	return ackKey{}, AckBinding{}, false
}

func (s *sessionAckKeys) add(key ackKey) {
	if s.many != nil {
		s.many[key] = struct{}{}
		return
	}
	if !s.singleSet {
		s.single = key
		s.singleSet = true
		return
	}
	if s.single == key {
		return
	}
	s.many = map[ackKey]struct{}{
		s.single: {},
		key:      {},
	}
	s.single = ackKey{}
	s.singleSet = false
}

func (s *sessionAckKeys) remove(key ackKey) {
	if s.many != nil {
		delete(s.many, key)
		switch len(s.many) {
		case 0:
			s.many = nil
		case 1:
			for remaining := range s.many {
				s.single = remaining
				s.singleSet = true
				s.many = nil
				break
			}
		}
		return
	}
	if s.singleSet && s.single == key {
		s.single = ackKey{}
		s.singleSet = false
	}
}

func (s sessionAckKeys) len() int {
	if s.many != nil {
		return len(s.many)
	}
	if s.singleSet {
		return 1
	}
	return 0
}

func (s sessionAckKeys) list(out []ackKey) []ackKey {
	if s.many != nil {
		for key := range s.many {
			out = append(out, key)
		}
		return out
	}
	if s.singleSet {
		out = append(out, s.single)
	}
	return out
}

func (s sessionAckKeys) findMessage(messageID uint64) (ackKey, bool) {
	if s.many != nil {
		for key := range s.many {
			if key.messageID == messageID {
				return key, true
			}
		}
		return ackKey{}, false
	}
	if s.singleSet && s.single.messageID == messageID {
		return s.single, true
	}
	return ackKey{}, false
}
