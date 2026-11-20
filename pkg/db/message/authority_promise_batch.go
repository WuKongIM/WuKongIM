package message

import (
	"context"
	"sort"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

// AuthorityPromiseBatchItem binds a promise to one independently owned lease.
type AuthorityPromiseBatchItem struct {
	Store   *ChannelStore
	Promise AuthorityPromise
}

// PromiseAuthorityBatch persists each physical store's admitted promises in one
// synchronous batch. Duplicate Channels are evaluated in input order. Leases
// and append locks remain owned through commit and cache publication, including
// when the caller cancels after physical persistence begins.
func PromiseAuthorityBatch(ctx context.Context, items []AuthorityPromiseBatchItem) []error {
	results := make([]error, len(items))
	groups := make(map[*MessageDB][]int)
	active := make([]*ChannelStore, 0, len(items))
	defer func() {
		for _, store := range active {
			store.endUse()
		}
	}()
	for i, item := range items {
		if err := ctxErr(ctx); err != nil {
			results[i] = err
		} else if item.Promise.IsZero() {
			results[i] = channel.ErrInvalidArgument
		} else if err := item.Store.beginUse(); err != nil {
			results[i] = err
		} else {
			active = append(active, item.Store)
			groups[item.Store.log.db] = append(groups[item.Store.log.db], i)
		}
	}
	for db, indexes := range groups {
		promiseAuthorityBatchOwner(ctx, db, items, indexes, results)
	}
	return results
}

func promiseAuthorityBatchOwner(ctx context.Context, db *MessageDB, items []AuthorityPromiseBatchItem, indexes []int, results []error) {
	entries := make([]*channelEntry, 0, len(indexes))
	seen := make(map[*channelEntry]bool, len(indexes))
	for _, index := range indexes {
		entry := items[index].Store.log.channelEntry
		if !seen[entry] {
			seen[entry] = true
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	if err := lockCommitEntriesWithoutHoldAndWait(ctx, entries, nil); err != nil {
		for _, index := range indexes {
			results[index] = err
		}
		return
	}
	defer func() {
		for _, entry := range entries {
			entry.appendMu.Unlock()
		}
	}()
	changes := make(map[*channelEntry]AuthorityPromise, len(entries))
	dependent := make([]int, 0, len(indexes))
	for _, index := range indexes {
		item := items[index]
		entry := item.Store.log.channelEntry
		if err := ctxErr(ctx); err != nil {
			results[index] = err
			continue
		}
		current, staged := changes[entry]
		present := staged
		if !staged {
			var err error
			current, present, err = entry.loadAuthorityPromiseLocked()
			if err != nil {
				results[index] = toChannelError(err)
				continue
			}
		}
		if present {
			if item.Promise.Compare(current) < 0 {
				if staged {
					durable, exists, err := entry.loadAuthorityPromiseLocked()
					if err != nil {
						results[index] = toChannelError(err)
						continue
					}
					if !exists || item.Promise.Compare(durable) >= 0 {
						// This rejection is valid only if the staged higher promise commits.
						dependent = append(dependent, index)
					}
				}
				results[index] = channel.ErrStaleAuthority
				continue
			}
			if item.Promise == current && !staged {
				continue // This equal promise was already durable before the batch.
			}
		}
		changes[entry] = item.Promise
		dependent = append(dependent, index)
	}
	if len(changes) == 0 {
		return
	}
	if err := commitAuthorityPromises(ctx, db, changes); err != nil {
		for _, index := range dependent {
			results[index] = toChannelError(err)
		}
	}
}

// commitAuthorityPromises publishes no cache state until all staged rows are
// durable. Its caller holds every affected append lock and database operation.
func commitAuthorityPromises(ctx context.Context, db *MessageDB, changes map[*channelEntry]AuthorityPromise) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	batch := db.engine.NewBatch()
	defer batch.Close()
	for entry, promise := range changes {
		if err := entry.stageAuthorityPromise(batch, promise); err != nil {
			return err
		}
		if err := entry.stageCatalog(batch); err != nil {
			return err
		}
	}
	if err := batch.Commit(true); err != nil {
		return err
	}
	for entry, promise := range changes {
		entry.publishAuthorityPromiseLocked(promise)
	}
	return nil
}
