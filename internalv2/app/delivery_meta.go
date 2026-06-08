package app

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const deliveryMetaMutationConcurrency = 32
const deliveryMetaSubscriberCacheMaxChannels = 4096
const deliveryMetaSubscriberCacheLoadPageSize = 1024

type deliveryMetaNode interface {
	Snapshot() clusterv2.Snapshot
	UpsertChannelMetadata(context.Context, metadb.Channel) error
	AddChannelSubscribers(context.Context, string, int64, []string, uint64) error
	ListChannelSubscribersPage(context.Context, string, int64, string, int) ([]string, string, bool, error)
}

type recipientSubscriberNode interface {
	ListChannelSubscribersPage(context.Context, string, int64, string, int) ([]string, string, bool, error)
}

// deliveryMetaStore adapts clusterv2 Slot metadata to bench setup and delivery fanout.
type deliveryMetaStore struct {
	// node owns the real replicated Slot metadata store.
	node    deliveryMetaNode
	version atomic.Uint64

	mu              sync.RWMutex
	subscriberCache map[deliveryMetaSubscriberKey]deliveryMetaSubscriberCacheEntry
}

func newDeliveryMetaStore(node deliveryMetaNode) *deliveryMetaStore {
	return &deliveryMetaStore{node: node}
}

func (s *deliveryMetaStore) UpsertChannels(ctx context.Context, mutations []accessapi.BenchChannelMutation) (int, error) {
	if s == nil || s.node == nil {
		return 0, nil
	}
	tasks := make([]func(context.Context) (int, error), 0, len(mutations))
	for _, mutation := range mutations {
		mutation := mutation
		tasks = append(tasks, func(ctx context.Context) (int, error) {
			if err := s.node.UpsertChannelMetadata(ctx, metadb.Channel{
				ChannelID:     mutation.ChannelID,
				ChannelType:   int64(mutation.ChannelType),
				Ban:           int64FromBool(mutation.Ban),
				Disband:       int64FromBool(mutation.Disband),
				SendBan:       int64FromBool(mutation.SendBan),
				AllowStranger: int64FromBool(mutation.AllowStranger),
			}); err != nil {
				return 0, err
			}
			return 1, nil
		})
	}
	return runDeliveryMetaTasks(ctx, tasks)
}

type deliveryMetaSubscriberKey struct {
	channelID   string
	channelType uint8
}

type deliveryMetaSubscriberCacheEntry struct {
	version uint64
	uids    []string
}

func (s *deliveryMetaStore) AddSubscribers(ctx context.Context, mutations []accessapi.BenchSubscriberMutation) (int, error) {
	if s == nil || s.node == nil {
		return 0, nil
	}
	groups := make(map[deliveryMetaSubscriberKey][]accessapi.BenchSubscriberMutation)
	order := make([]deliveryMetaSubscriberKey, 0, len(mutations))
	for _, mutation := range mutations {
		key := deliveryMetaSubscriberKey{channelID: mutation.ChannelID, channelType: mutation.ChannelType}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], mutation)
	}
	tasks := make([]func(context.Context) (int, error), 0, len(order))
	for _, key := range order {
		group := append([]accessapi.BenchSubscriberMutation(nil), groups[key]...)
		tasks = append(tasks, func(ctx context.Context) (int, error) {
			accepted := 0
			for _, mutation := range group {
				version := s.version.Add(1)
				if err := s.node.AddChannelSubscribers(ctx, mutation.ChannelID, int64(mutation.ChannelType), append([]string(nil), mutation.Subscribers...), version); err != nil {
					return accepted, err
				}
				accepted += len(mutation.Subscribers)
			}
			return accepted, nil
		})
	}
	return runDeliveryMetaTasks(ctx, tasks)
}

func runDeliveryMetaTasks(ctx context.Context, tasks []func(context.Context) (int, error)) (int, error) {
	if len(tasks) == 0 {
		return 0, nil
	}
	workers := deliveryMetaMutationConcurrency
	if workers > len(tasks) {
		workers = len(tasks)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan func(context.Context) (int, error))
	var accepted atomic.Int64
	var firstErr error
	var firstErrOnce sync.Once
	setErr := func(err error) {
		if err == nil {
			return
		}
		firstErrOnce.Do(func() {
			firstErr = err
			cancel()
		})
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range jobs {
				n, err := task(workCtx)
				if n > 0 {
					accepted.Add(int64(n))
				}
				if err != nil {
					setErr(err)
				}
			}
		}()
	}
send:
	for _, task := range tasks {
		select {
		case <-workCtx.Done():
			break send
		case jobs <- task:
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return int(accepted.Load()), firstErr
	}
	if err := ctx.Err(); err != nil {
		return int(accepted.Load()), err
	}
	return int(accepted.Load()), nil
}

func (s *deliveryMetaStore) ListSubscribers(ctx context.Context, req runtimedelivery.SubscriberPageRequest) (runtimedelivery.UIDPage, error) {
	if s == nil || s.node == nil {
		return runtimedelivery.UIDPage{Done: true}, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 1
	}
	key := deliveryMetaSubscriberKey{channelID: req.ChannelID, channelType: req.ChannelType}
	snapshot, err := s.subscriberSnapshot(ctx, key)
	if err != nil {
		return runtimedelivery.UIDPage{}, err
	}
	filtered := s.filterPartition(snapshot, req.Partition)
	return subscriberPageFromSnapshot(filtered, req.Cursor, limit)
}

// NextPage pages durable channel subscribers for recipient-authority dispatch without partition filtering.
func (s *deliveryMetaStore) NextPage(ctx context.Context, event messageevents.MessageCommitted, cursor string, limit int) (recipientusecase.RecipientPage, error) {
	if s == nil || s.node == nil {
		return recipientusecase.RecipientPage{Done: true}, nil
	}
	return nextRecipientSubscriberPage(ctx, s.node, event, cursor, limit)
}

type recipientSubscriberStore struct {
	node recipientSubscriberNode
}

func (s recipientSubscriberStore) NextPage(ctx context.Context, event messageevents.MessageCommitted, cursor string, limit int) (recipientusecase.RecipientPage, error) {
	if s.node == nil {
		return recipientusecase.RecipientPage{Done: true}, nil
	}
	return nextRecipientSubscriberPage(ctx, s.node, event, cursor, limit)
}

func nextRecipientSubscriberPage(ctx context.Context, node recipientSubscriberNode, event messageevents.MessageCommitted, cursor string, limit int) (recipientusecase.RecipientPage, error) {
	if limit <= 0 {
		limit = 1
	}
	uids, nextCursor, done, err := node.ListChannelSubscribersPage(ctx, event.ChannelID, int64(event.ChannelType), cursor, limit)
	if err != nil {
		return recipientusecase.RecipientPage{}, err
	}
	recipients := make([]recipientusecase.Recipient, 0, len(uids))
	for _, uid := range uids {
		if uid != "" {
			recipients = append(recipients, recipientusecase.Recipient{UID: uid})
		}
	}
	return recipientusecase.RecipientPage{Recipients: recipients, Cursor: nextCursor, Done: done}, nil
}

func (s *deliveryMetaStore) subscriberSnapshot(ctx context.Context, key deliveryMetaSubscriberKey) ([]string, error) {
	version := s.version.Load()
	if uids, ok := s.cachedSubscribers(key, version); ok {
		return uids, nil
	}
	uids, err := s.loadSubscriberSnapshot(ctx, key)
	if err != nil {
		return nil, err
	}
	return s.storeSubscriberSnapshot(key, version, uids), nil
}

func (s *deliveryMetaStore) cachedSubscribers(key deliveryMetaSubscriberKey, version uint64) ([]string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.subscriberCache[key]
	if !ok || entry.version != version {
		return nil, false
	}
	return entry.uids, true
}

func (s *deliveryMetaStore) storeSubscriberSnapshot(key deliveryMetaSubscriberKey, version uint64, uids []string) []string {
	snapshot := append([]string(nil), uids...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subscriberCache == nil {
		s.subscriberCache = make(map[deliveryMetaSubscriberKey]deliveryMetaSubscriberCacheEntry)
	}
	if _, ok := s.subscriberCache[key]; !ok && len(s.subscriberCache) >= deliveryMetaSubscriberCacheMaxChannels {
		s.subscriberCache = make(map[deliveryMetaSubscriberKey]deliveryMetaSubscriberCacheEntry)
	}
	s.subscriberCache[key] = deliveryMetaSubscriberCacheEntry{version: version, uids: snapshot}
	return snapshot
}

func (s *deliveryMetaStore) loadSubscriberSnapshot(ctx context.Context, key deliveryMetaSubscriberKey) ([]string, error) {
	var out []string
	cursor := ""
	for {
		uids, nextCursor, done, err := s.node.ListChannelSubscribersPage(ctx, key.channelID, int64(key.channelType), cursor, deliveryMetaSubscriberCacheLoadPageSize)
		if err != nil {
			return nil, err
		}
		out = append(out, uids...)
		if done {
			return out, nil
		}
		if nextCursor == "" || nextCursor == cursor {
			return nil, runtimedelivery.ErrInvalidSubscriberCursor
		}
		cursor = nextCursor
	}
}

func subscriberPageFromSnapshot(uids []string, cursor string, limit int) (runtimedelivery.UIDPage, error) {
	start := 0
	if cursor != "" {
		offset, err := strconv.Atoi(cursor)
		if err != nil || offset < 0 {
			return runtimedelivery.UIDPage{}, runtimedelivery.ErrInvalidSubscriberCursor
		}
		start = offset
	}
	if start >= len(uids) {
		return runtimedelivery.UIDPage{Done: true}, nil
	}
	end := start + limit
	if end >= len(uids) {
		return runtimedelivery.UIDPage{UIDs: append([]string(nil), uids[start:]...), Done: true}, nil
	}
	return runtimedelivery.UIDPage{
		UIDs:       append([]string(nil), uids[start:end]...),
		NextCursor: strconv.Itoa(end),
	}, nil
}

func (s *deliveryMetaStore) filterPartition(uids []string, partition runtimedelivery.Partition) []string {
	count := uint16(0)
	if s != nil && s.node != nil {
		count = s.node.Snapshot().HashSlotCount
	}
	if count == 0 || isDefaultDeliveryPartition(partition) {
		return uids
	}
	filtered := make([]string, 0, len(uids))
	for _, uid := range uids {
		slot := routing.HashSlotForKey(uid, count)
		if slot >= partition.HashSlotStart && slot <= partition.HashSlotEnd {
			filtered = append(filtered, uid)
		}
	}
	return filtered
}

func isDefaultDeliveryPartition(partition runtimedelivery.Partition) bool {
	return partition.LeaderNodeID == 0 && partition.HashSlotStart == 0 && partition.HashSlotEnd == 0
}

func int64FromBool(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
