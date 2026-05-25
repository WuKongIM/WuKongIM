package cmdsync

import (
	"context"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultPendingFlushInterval = time.Second
	defaultPendingFlushBatch    = 500
	defaultPendingShardCount    = 16
)

// ConversationUpdaterOptions configures the owner-local pending CMD conversation updater.
type ConversationUpdaterOptions struct {
	// Store persists flushed pending state into the durable CMD state store.
	Store PendingStateStore
	// DataDir stores pending updates across graceful restarts when non-empty.
	DataDir string
	// FlushInterval controls the background durable flush cadence.
	FlushInterval time.Duration
	// FlushBatchSize limits one all-or-error durable upsert batch.
	FlushBatchSize int
	// ShardCount controls the number of locked in-memory pending shards.
	ShardCount int
	// Now returns the current time used for durable UpdatedAt timestamps.
	Now func() time.Time
	// Logger receives non-fatal pending file and background flush diagnostics.
	Logger wklog.Logger
}

// ConversationUpdater buffers owner-local CMD conversation state before durable flush.
type ConversationUpdater struct {
	store          PendingStateStore
	dataDir        string
	flushInterval  time.Duration
	flushBatchSize int
	now            func() time.Time
	logger         wklog.Logger

	shards []conversationPendingShard

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}
	cancel  context.CancelFunc
	// restoredFileDirty keeps a loaded pending file until durable flush/save succeeds.
	restoredFileDirty bool
}

type conversationPendingShard struct {
	mu               sync.Mutex
	pendingByChannel map[CommandChannelKey]*PendingConversationUpdate
	userIndex        map[string]map[CommandChannelKey]struct{}
}

type pendingFlushEntry struct {
	state      metadb.CMDConversationState
	lastMsgSeq uint64
}

// NewConversationUpdater creates a pending updater with safe local defaults.
func NewConversationUpdater(opts ConversationUpdaterOptions) *ConversationUpdater {
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = defaultPendingFlushInterval
	}
	if opts.FlushBatchSize <= 0 {
		opts.FlushBatchSize = defaultPendingFlushBatch
	}
	if opts.ShardCount <= 0 {
		opts.ShardCount = defaultPendingShardCount
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}

	u := &ConversationUpdater{
		store:          opts.Store,
		dataDir:        opts.DataDir,
		flushInterval:  opts.FlushInterval,
		flushBatchSize: opts.FlushBatchSize,
		now:            opts.Now,
		logger:         opts.Logger,
		shards:         make([]conversationPendingShard, opts.ShardCount),
	}
	for i := range u.shards {
		u.shards[i].pendingByChannel = make(map[CommandChannelKey]*PendingConversationUpdate)
		u.shards[i].userIndex = make(map[string]map[CommandChannelKey]struct{})
	}
	return u
}

// Start loads pending updates from disk and starts the background flush loop.
func (u *ConversationUpdater) Start() error {
	if u == nil {
		return nil
	}
	u.mu.Lock()
	if u.running {
		u.mu.Unlock()
		return nil
	}
	if err := u.loadPendingFile(); err != nil {
		u.mu.Unlock()
		return err
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	runCtx, cancel := context.WithCancel(context.Background())
	u.stopCh = stopCh
	u.doneCh = doneCh
	u.cancel = cancel
	u.running = true
	interval := u.flushInterval
	u.mu.Unlock()

	go u.flushLoop(runCtx, stopCh, doneCh, interval)
	return nil
}

// Stop stops the background loop, flushes running updaters, and saves remaining pending updates.
func (u *ConversationUpdater) Stop() error {
	return u.StopContext(context.Background())
}

// StopContext stops periodic flushing and bounds the final flush with ctx.
func (u *ConversationUpdater) StopContext(ctx context.Context) error {
	if u == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	u.mu.Lock()
	wasRunning := u.running
	stopCh := u.stopCh
	doneCh := u.doneCh
	cancel := u.cancel
	if wasRunning {
		u.running = false
		u.stopCh = nil
		u.doneCh = nil
		u.cancel = nil
	}
	u.mu.Unlock()

	if wasRunning {
		if cancel != nil {
			cancel()
		}
		close(stopCh)
		select {
		case <-doneCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	var flushErr error
	if wasRunning {
		flushErr = u.Flush(ctx)
	}
	saveErr := u.savePendingFile()
	if flushErr != nil {
		return flushErr
	}
	return saveErr
}

// PushIntent merges one validated conversation intent into the owner-local pending buffer.
func (u *ConversationUpdater) PushIntent(_ context.Context, intent ConversationIntent) error {
	if u == nil {
		return ErrIntentRequired
	}
	key, readSeqs, err := normalizeConversationIntent(intent)
	if err != nil {
		return err
	}

	shard := u.shardForKey(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	update := shard.pendingByChannel[key]
	if update == nil {
		update = &PendingConversationUpdate{
			CommandChannelID: key.ChannelID,
			ChannelType:      key.ChannelType,
			LastMsgSeq:       intent.MessageSeq,
			ActiveAt:         intent.ActiveAt,
			UserReadSeqs:     make(map[string]uint64, len(readSeqs)),
			UserLastMsgSeqs:  make(map[string]uint64, len(readSeqs)),
			UserActiveAts:    make(map[string]int64, len(readSeqs)),
		}
		shard.pendingByChannel[key] = update
	} else {
		if intent.MessageSeq > update.LastMsgSeq {
			update.LastMsgSeq = intent.MessageSeq
		}
		if intent.ActiveAt > update.ActiveAt {
			update.ActiveAt = intent.ActiveAt
		}
	}

	for uid, readSeq := range readSeqs {
		if current, ok := update.UserReadSeqs[uid]; !ok || readSeq > current {
			update.UserReadSeqs[uid] = readSeq
		}
		if update.UserLastMsgSeqs == nil {
			update.UserLastMsgSeqs = make(map[string]uint64, len(update.UserReadSeqs))
		}
		if current, ok := update.UserLastMsgSeqs[uid]; !ok || intent.MessageSeq > current {
			update.UserLastMsgSeqs[uid] = intent.MessageSeq
		}
		if update.UserActiveAts == nil {
			update.UserActiveAts = make(map[string]int64, len(update.UserReadSeqs))
		}
		if current, ok := update.UserActiveAts[uid]; !ok || intent.ActiveAt > current {
			update.UserActiveAts[uid] = intent.ActiveAt
		}
		if shard.userIndex[uid] == nil {
			shard.userIndex[uid] = make(map[CommandChannelKey]struct{})
		}
		shard.userIndex[uid][key] = struct{}{}
	}
	return nil
}

// ListPending returns deterministic pending overlays for one UID.
func (u *ConversationUpdater) ListPending(_ context.Context, uid string, limit int) []PendingConversationView {
	if u == nil {
		return nil
	}
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return nil
	}

	views := make([]PendingConversationView, 0)
	for i := range u.shards {
		shard := &u.shards[i]
		shard.mu.Lock()
		keys := shard.userIndex[uid]
		for key := range keys {
			update := shard.pendingByChannel[key]
			if update == nil {
				continue
			}
			readSeq, ok := update.UserReadSeqs[uid]
			if !ok {
				continue
			}
			views = append(views, PendingConversationView{
				CommandChannelID: update.CommandChannelID,
				ChannelType:      update.ChannelType,
				LastMsgSeq:       pendingUserLastMsgSeq(update, uid),
				ActiveAt:         pendingUserActiveAt(update, uid),
				ReadSeq:          readSeq,
			})
		}
		shard.mu.Unlock()
	}

	sortPendingViews(views)
	if limit > 0 && len(views) > limit {
		views = views[:limit]
	}
	return views
}

// MarkSynced removes one UID's pending overlay after durable read progress covers it.
func (u *ConversationUpdater) MarkSynced(_ context.Context, uid string, key CommandChannelKey, throughSeq uint64) error {
	if u == nil {
		return nil
	}
	uid = strings.TrimSpace(uid)
	key.ChannelID = strings.TrimSpace(key.ChannelID)
	if uid == "" || key.ChannelID == "" || key.ChannelType == 0 {
		return nil
	}

	shard := u.shardForKey(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	update := shard.pendingByChannel[key]
	if update == nil || throughSeq < pendingUserLastMsgSeq(update, uid) {
		return nil
	}
	u.removeUIDLocked(shard, uid, key)
	return nil
}

// Flush persists pending UID/channel states in all-or-error batches.
func (u *ConversationUpdater) Flush(ctx context.Context) error {
	if u == nil {
		return nil
	}
	if u.store == nil {
		return ErrStateStoreRequired
	}
	entries := u.snapshotFlushEntries()
	if len(entries) == 0 {
		return u.saveRestoredPendingFileIfNeeded()
	}

	batchSize := u.flushBatchSize
	if batchSize <= 0 {
		batchSize = defaultPendingFlushBatch
	}
	for start := 0; start < len(entries); start += batchSize {
		end := start + batchSize
		if end > len(entries) {
			end = len(entries)
		}
		batch := entries[start:end]
		states := make([]metadb.CMDConversationState, 0, len(batch))
		for _, entry := range batch {
			states = append(states, entry.state)
		}
		if err := u.store.UpsertCMDConversationStates(ctx, states); err != nil {
			return err
		}
		u.removeFlushedEntries(batch)
	}
	return u.saveRestoredPendingFileIfNeeded()
}

func (u *ConversationUpdater) flushLoop(ctx context.Context, stopCh <-chan struct{}, doneCh chan<- struct{}, interval time.Duration) {
	defer close(doneCh)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := u.Flush(ctx); err != nil && u.logger != nil {
				u.logger.Warn("flush pending CMD conversation updates failed", wklog.Error(err))
			}
		case <-stopCh:
			return
		}
	}
}

func normalizeConversationIntent(intent ConversationIntent) (CommandChannelKey, map[string]uint64, error) {
	key := CommandChannelKey{ChannelID: strings.TrimSpace(intent.CommandChannelID), ChannelType: intent.ChannelType}
	if intent.MessageSeq == 0 || key.ChannelID == "" || key.ChannelType == 0 || !runtimechannelid.IsCommandChannel(key.ChannelID) {
		return CommandChannelKey{}, nil, ErrIntentRequired
	}
	readSeqs := make(map[string]uint64, len(intent.UserReadSeqs))
	for uid, readSeq := range intent.UserReadSeqs {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		if current, ok := readSeqs[uid]; !ok || readSeq > current {
			readSeqs[uid] = readSeq
		}
	}
	if len(readSeqs) == 0 {
		return CommandChannelKey{}, nil, ErrIntentRequired
	}
	return key, readSeqs, nil
}

func (u *ConversationUpdater) snapshotFlushEntries() []pendingFlushEntry {
	updatedAt := u.now().UnixNano()
	entries := make([]pendingFlushEntry, 0)
	for i := range u.shards {
		shard := &u.shards[i]
		shard.mu.Lock()
		keys := sortedPendingKeys(shard.pendingByChannel)
		for _, key := range keys {
			update := shard.pendingByChannel[key]
			if update == nil {
				continue
			}
			uids := sortedUIDs(update.UserReadSeqs)
			for _, uid := range uids {
				entries = append(entries, pendingFlushEntry{
					state: metadb.CMDConversationState{
						UID:         uid,
						ChannelID:   update.CommandChannelID,
						ChannelType: int64(update.ChannelType),
						ReadSeq:     update.UserReadSeqs[uid],
						ActiveAt:    pendingUserActiveAt(update, uid),
						UpdatedAt:   updatedAt,
					},
					lastMsgSeq: pendingUserLastMsgSeq(update, uid),
				})
			}
		}
		shard.mu.Unlock()
	}
	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i].state, entries[j].state
		if left.ActiveAt != right.ActiveAt {
			return left.ActiveAt < right.ActiveAt
		}
		if left.ChannelType != right.ChannelType {
			return left.ChannelType < right.ChannelType
		}
		if left.ChannelID != right.ChannelID {
			return left.ChannelID < right.ChannelID
		}
		return left.UID < right.UID
	})
	return entries
}

func (u *ConversationUpdater) removeFlushedEntries(entries []pendingFlushEntry) {
	for _, entry := range entries {
		key := CommandChannelKey{ChannelID: entry.state.ChannelID, ChannelType: uint8(entry.state.ChannelType)}
		shard := u.shardForKey(key)
		shard.mu.Lock()
		update := shard.pendingByChannel[key]
		if update != nil &&
			pendingUserLastMsgSeq(update, entry.state.UID) <= entry.lastMsgSeq &&
			pendingUserActiveAt(update, entry.state.UID) <= entry.state.ActiveAt &&
			update.UserReadSeqs[entry.state.UID] <= entry.state.ReadSeq {
			u.removeUIDLocked(shard, entry.state.UID, key)
		}
		shard.mu.Unlock()
	}
}

func (u *ConversationUpdater) removeUIDLocked(shard *conversationPendingShard, uid string, key CommandChannelKey) {
	update := shard.pendingByChannel[key]
	if update != nil {
		delete(update.UserReadSeqs, uid)
		delete(update.UserLastMsgSeqs, uid)
		delete(update.UserActiveAts, uid)
		if len(update.UserReadSeqs) == 0 {
			delete(shard.pendingByChannel, key)
		}
	}
	if keys := shard.userIndex[uid]; keys != nil {
		delete(keys, key)
		if len(keys) == 0 {
			delete(shard.userIndex, uid)
		}
	}
}

func (u *ConversationUpdater) markRestoredPendingFileDirty() {
	// loadPendingFile runs during Start while u.mu is already held.
	u.restoredFileDirty = true
}

func (u *ConversationUpdater) saveRestoredPendingFileIfNeeded() error {
	u.mu.Lock()
	dirty := u.restoredFileDirty
	u.mu.Unlock()
	if !dirty {
		return nil
	}
	if err := u.savePendingFile(); err != nil {
		return err
	}
	hasPending := len(u.snapshotUpdates()) > 0
	u.mu.Lock()
	u.restoredFileDirty = hasPending
	u.mu.Unlock()
	return nil
}

func (u *ConversationUpdater) putLoadedUpdate(update PendingConversationUpdate) {
	for uid, readSeq := range update.UserReadSeqs {
		lastMsgSeq := update.UserLastMsgSeqs[uid]
		if lastMsgSeq == 0 {
			lastMsgSeq = update.LastMsgSeq
		}
		activeAt := update.UserActiveAts[uid]
		if activeAt == 0 {
			activeAt = update.ActiveAt
		}
		intent := ConversationIntent{
			CommandChannelID: update.CommandChannelID,
			ChannelType:      update.ChannelType,
			MessageSeq:       lastMsgSeq,
			ActiveAt:         activeAt,
			UserReadSeqs:     map[string]uint64{uid: readSeq},
		}
		_ = u.PushIntent(context.Background(), intent)
	}
}

func (u *ConversationUpdater) snapshotUpdates() []PendingConversationUpdate {
	updates := make([]PendingConversationUpdate, 0)
	for i := range u.shards {
		shard := &u.shards[i]
		shard.mu.Lock()
		keys := sortedPendingKeys(shard.pendingByChannel)
		for _, key := range keys {
			update := shard.pendingByChannel[key]
			if update == nil || len(update.UserReadSeqs) == 0 {
				continue
			}
			copyUpdate := PendingConversationUpdate{
				CommandChannelID: update.CommandChannelID,
				ChannelType:      update.ChannelType,
				LastMsgSeq:       update.LastMsgSeq,
				ActiveAt:         update.ActiveAt,
				UserReadSeqs:     make(map[string]uint64, len(update.UserReadSeqs)),
				UserLastMsgSeqs:  make(map[string]uint64, len(update.UserReadSeqs)),
				UserActiveAts:    make(map[string]int64, len(update.UserReadSeqs)),
			}
			for uid, readSeq := range update.UserReadSeqs {
				copyUpdate.UserReadSeqs[uid] = readSeq
				copyUpdate.UserLastMsgSeqs[uid] = pendingUserLastMsgSeq(update, uid)
				copyUpdate.UserActiveAts[uid] = pendingUserActiveAt(update, uid)
			}
			updates = append(updates, copyUpdate)
		}
		shard.mu.Unlock()
	}
	sort.Slice(updates, func(i, j int) bool {
		if updates[i].ChannelType != updates[j].ChannelType {
			return updates[i].ChannelType < updates[j].ChannelType
		}
		return updates[i].CommandChannelID < updates[j].CommandChannelID
	})
	return updates
}

func (u *ConversationUpdater) shardForKey(key CommandChannelKey) *conversationPendingShard {
	idx := hashCommandChannelKey(key) % uint32(len(u.shards))
	return &u.shards[idx]
}

func hashCommandChannelKey(key CommandChannelKey) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key.ChannelID))
	_, _ = h.Write([]byte{key.ChannelType})
	return h.Sum32()
}

func sortPendingViews(views []PendingConversationView) {
	sort.Slice(views, func(i, j int) bool {
		if views[i].ActiveAt != views[j].ActiveAt {
			return views[i].ActiveAt > views[j].ActiveAt
		}
		if views[i].ChannelType != views[j].ChannelType {
			return views[i].ChannelType < views[j].ChannelType
		}
		return views[i].CommandChannelID < views[j].CommandChannelID
	})
}

func sortedPendingKeys(updates map[CommandChannelKey]*PendingConversationUpdate) []CommandChannelKey {
	keys := make([]CommandChannelKey, 0, len(updates))
	for key := range updates {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ChannelType != keys[j].ChannelType {
			return keys[i].ChannelType < keys[j].ChannelType
		}
		return keys[i].ChannelID < keys[j].ChannelID
	})
	return keys
}

func sortedUIDs(readSeqs map[string]uint64) []string {
	uids := make([]string, 0, len(readSeqs))
	for uid := range readSeqs {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	return uids
}

func pendingUserLastMsgSeq(update *PendingConversationUpdate, uid string) uint64 {
	if update == nil {
		return 0
	}
	if seq := update.UserLastMsgSeqs[uid]; seq > 0 {
		return seq
	}
	return update.LastMsgSeq
}

func pendingUserActiveAt(update *PendingConversationUpdate, uid string) int64 {
	if update == nil {
		return 0
	}
	if activeAt := update.UserActiveAts[uid]; activeAt > 0 {
		return activeAt
	}
	return update.ActiveAt
}
