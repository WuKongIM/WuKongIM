package message

import (
	"sort"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// commitOwnership owns canonical locks and background pins after commit admission begins.
type commitOwnership struct {
	// once makes terminal lock and pin release idempotent.
	once sync.Once
	// registry owns every background pin in pins.
	registry *channelRegistry
	// pins keep canonical entries alive until commit terminalization.
	pins []*channelEntry
	// appendLocks are transferred from preparation to the commit finalizer.
	appendLocks []*channelEntry
	// checkpointLocks are optional locks transferred with apply requests.
	checkpointLocks []*channelEntry
}

func newCommitOwnership(registry *channelRegistry, appendLocks, checkpointLocks []*channelEntry) (*commitOwnership, error) {
	return newCommitOwnershipWithPins(registry, appendLocks, appendLocks, checkpointLocks)
}

func newCheckpointCommitOwnership(registry *channelRegistry, checkpointLocks []*channelEntry) (*commitOwnership, error) {
	return newCommitOwnershipWithPins(registry, checkpointLocks, nil, checkpointLocks)
}

func newCommitOwnershipWithPins(registry *channelRegistry, pinEntries, appendLocks, checkpointLocks []*channelEntry) (*commitOwnership, error) {
	ownership := &commitOwnership{
		registry:        registry,
		appendLocks:     appendLocks,
		checkpointLocks: checkpointLocks,
	}
	if registry == nil {
		ownership.finalize()
		return nil, dberrors.ErrClosed
	}
	for _, entry := range pinEntries {
		if err := registry.retainPin(entry); err != nil {
			ownership.finalize()
			return nil, err
		}
		ownership.pins = append(ownership.pins, entry)
	}
	return ownership, nil
}

func (o *commitOwnership) finalize() {
	if o == nil {
		return
	}
	o.once.Do(func() {
		for i := len(o.checkpointLocks) - 1; i >= 0; i-- {
			o.checkpointLocks[i].checkpointMu.Unlock()
		}
		for i := len(o.appendLocks) - 1; i >= 0; i-- {
			o.appendLocks[i].appendMu.Unlock()
		}
		for i := len(o.pins) - 1; i >= 0; i-- {
			o.registry.releasePin(o.pins[i])
		}
	})
}

func preparedCommitEntries(prepared []preparedCommitRows) (appendEntries, checkpointEntries []*channelEntry, duplicate bool) {
	seenAppend := make(map[*channelEntry]struct{}, len(prepared))
	seenCheckpoint := make(map[*channelEntry]struct{}, len(prepared))
	for _, item := range prepared {
		if item.store == nil || item.store.log == nil || item.store.log.channelEntry == nil {
			duplicate = true
			continue
		}
		entry := item.store.log.channelEntry
		if _, ok := seenAppend[entry]; ok {
			duplicate = true
		} else {
			seenAppend[entry] = struct{}{}
			appendEntries = append(appendEntries, entry)
		}
		if item.checkpointLocked || item.checkpoint != nil {
			if _, ok := seenCheckpoint[entry]; !ok {
				seenCheckpoint[entry] = struct{}{}
				checkpointEntries = append(checkpointEntries, entry)
			}
		}
	}
	sort.Slice(appendEntries, func(i, j int) bool { return appendEntries[i].key < appendEntries[j].key })
	sort.Slice(checkpointEntries, func(i, j int) bool { return checkpointEntries[i].key < checkpointEntries[j].key })
	return appendEntries, checkpointEntries, duplicate
}

func unlockCommitEntries(appendEntries, checkpointEntries []*channelEntry) {
	for i := len(checkpointEntries) - 1; i >= 0; i-- {
		checkpointEntries[i].checkpointMu.Unlock()
	}
	for i := len(appendEntries) - 1; i >= 0; i-- {
		appendEntries[i].appendMu.Unlock()
	}
}
