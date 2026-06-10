package conversationactive

import (
	"sync"
	"time"
)

type conversationKey struct {
	channelID   string
	channelType uint8
}

// Manager owns the in-memory UID conversation active cache.
type Manager struct {
	// mu protects cache and all per-UID conversation rows.
	mu sync.RWMutex
	// now supplies ActiveAt when an admitted batch does not provide one.
	now func() time.Time
	// cache stores UID -> conversation key -> active projection.
	cache map[string]map[conversationKey]ActivePatch
}

// NewManager creates a conversation active admission manager.
func NewManager(opts Options) *Manager {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		now:   now,
		cache: make(map[string]map[conversationKey]ActivePatch),
	}
}

// AdmitActiveBatch admits a channelwrite recipient batch into the active cache.
func (m *Manager) AdmitActiveBatch(batch ActiveBatch) error {
	activeAt := batch.ActiveAt
	if activeAt.IsZero() {
		activeAt = m.now()
	}

	patches := make([]ActivePatch, 0, len(batch.Recipients)+1)
	if batch.SenderUID != "" {
		patches = append(patches, ActivePatch{
			UID:         batch.SenderUID,
			ChannelID:   batch.ChannelID,
			ChannelType: batch.ChannelType,
			ActiveAt:    activeAt,
			ReadSeq:     batch.MessageSeq,
		})
	}

	for _, recipient := range batch.Recipients {
		if recipient.UID == "" {
			continue
		}
		if batch.SenderUID != "" && recipient.UID == batch.SenderUID {
			continue
		}

		var readSeq uint64
		if recipient.IsSender {
			readSeq = batch.MessageSeq
		}

		patches = append(patches, ActivePatch{
			UID:         recipient.UID,
			ChannelID:   batch.ChannelID,
			ChannelType: batch.ChannelType,
			ActiveAt:    activeAt,
			ReadSeq:     readSeq,
		})
	}

	return m.MarkActive(patches)
}

// MarkActive merges active conversation patches into the UID cache.
func (m *Manager) MarkActive(patches []ActivePatch) error {
	if len(patches) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, patch := range patches {
		if patch.UID == "" {
			continue
		}
		m.markActiveLocked(patch)
	}
	return nil
}

func (m *Manager) markActiveLocked(patch ActivePatch) {
	key := conversationKey{channelID: patch.ChannelID, channelType: patch.ChannelType}
	byChannel := m.cache[patch.UID]
	if byChannel == nil {
		byChannel = make(map[conversationKey]ActivePatch)
		m.cache[patch.UID] = byChannel
	}

	current, ok := byChannel[key]
	if !ok {
		byChannel[key] = patch
		return
	}

	if patch.ActiveAt.After(current.ActiveAt) {
		current.ActiveAt = patch.ActiveAt
	}
	if patch.ReadSeq > current.ReadSeq {
		current.ReadSeq = patch.ReadSeq
	}
	byChannel[key] = current
}

// EntryForTest returns a cached active row for tests.
func (m *Manager) EntryForTest(uid, channelID string, channelType uint8) (ActivePatch, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	byChannel := m.cache[uid]
	if byChannel == nil {
		return ActivePatch{}, false
	}
	entry, ok := byChannel[conversationKey{channelID: channelID, channelType: channelType}]
	return entry, ok
}
