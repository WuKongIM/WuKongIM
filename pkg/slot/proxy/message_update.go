package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"slices"
	"sort"
	"sync"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const messageUpdateRPCServiceID = clusternet.RPCSlotMessageUpdates

// messageUpdateReadWorkers overlaps independent quorum waits while bounding
// per-request goroutines and outstanding Slot RPCs, even with 256 hash slots.
const messageUpdateReadWorkers = 8

type messageUpdateReadRPC struct {
	Probe  bool `json:"Probe,omitempty"`
	Format int
	SlotID uint64                     `json:"SlotID,omitempty"`
	Reads  []metadb.MessageUpdateRead `json:"Reads,omitempty"`
}
type messageUpdateReadReply struct {
	Format   int
	Status   string
	LeaderID uint64                     `json:"LeaderID,omitempty"`
	Pages    []metadb.MessageUpdatePage `json:"Pages,omitempty"`
}

func (r messageUpdateReadReply) rpcStatus() string   { return r.Status }
func (r messageUpdateReadReply) rpcLeaderID() uint64 { return r.LeaderID }

// ApplyMessageUpdate submits an edit through the existing physical Slot Raft
// path. Deterministic business outcomes remain distinct from uncertain writes.
func (s *Store) ApplyMessageUpdate(ctx context.Context, q metadb.MessageUpdateMutation) (metadb.MessageUpdateMutationResult, error) {
	var out metadb.MessageUpdateMutationResult
	proof, err := s.checkMessageUpdateReplicas(ctx, q.ChannelID, q.ReplicaSet)
	if err != nil {
		return out, err
	}
	q.ReplicaSet = proof
	cmd, err := metafsm.EncodeMessageUpdateCommand(q)
	if err != nil {
		return out, err
	}
	data, err := proposeWithHashSlotResult(ctx, s.cluster, s.cluster.SlotForKey(q.ChannelID), hashSlotForKey(s.cluster, q.ChannelID), cmd)
	if err != nil {
		return out, err
	}
	if string(data) == metafsm.ApplyResultHashSlotFenced || string(data) == metafsm.ApplyResultStaleMeta {
		return out, metadb.ErrStaleMeta
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	if out.Status == "" {
		return out, metadb.ErrCorruptValue
	}
	return out, nil
}

// ReadMessageUpdatesBatch groups bounded reads by physical Slot and uses one
// fresh quorum/applied barrier per group. It never caches negative edit reads.
func (s *Store) ReadMessageUpdatesBatch(ctx context.Context, reads []metadb.MessageUpdateRead) ([]metadb.MessageUpdatePage, error) {
	if len(reads) > metadb.MaxMessageUpdatePage {
		return nil, metadb.ErrInvalidArgument
	}
	out := make([]metadb.MessageUpdatePage, len(reads))
	if len(reads) == 0 {
		return out, nil
	}
	totalTargets := 0
	groups := map[multiraft.SlotID][]int{}
	for i, q := range reads {
		totalTargets += max(1, max(len(q.IDs), q.Limit))
		if totalTargets > metadb.MaxMessageUpdatePage {
			return nil, metadb.ErrInvalidArgument
		}
		slot := s.cluster.SlotForKey(q.ChannelID)
		groups[slot] = append(groups[slot], i)
	}
	slots := make([]multiraft.SlotID, 0, len(groups))
	for slot := range groups {
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var retainedMu sync.Mutex
	retainedBytes := 0
	budgetExceeded := false
	errs := make([]error, len(slots))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < min(messageUpdateReadWorkers, len(slots)); worker++ {
		wg.Add(1)
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskSlotMessageUpdateRead, func() {
			defer wg.Done()
			for g := range jobs {
				slot := slots[g]
				req := messageUpdateReadRPC{Format: 1, SlotID: uint64(slot)}
				for _, i := range groups[slot] {
					req.Reads = append(req.Reads, reads[i])
				}
				var reply messageUpdateReadReply
				var err error
				if s.shouldServeSlotLocally(slot) {
					reply, err = s.readMessageUpdatesLocal(ctx, req)
				} else {
					body, _ := json.Marshal(req)
					reply, err = callAuthoritativeRPC(ctx, s, slot, messageUpdateRPCServiceID, body, decodeMessageUpdateReply)
				}
				if err == nil && (reply.Status != rpcStatusOK || len(reply.Pages) != len(req.Reads)) {
					err = metadb.ErrStaleMeta
				}
				if err != nil {
					errs[g] = err
					continue
				}
				size := 0
				for _, page := range reply.Pages {
					for _, row := range page.Updates {
						size += len(row.Payload) + len(row.ChannelID) + len(row.PendingAfterUID) + 128
					}
				}
				retainedMu.Lock()
				if retainedBytes+size > metadb.MaxMessageUpdatePageBytes {
					budgetExceeded = true
					cancel()
				} else if !budgetExceeded {
					retainedBytes += size
					for j, i := range groups[slot] {
						out[i] = reply.Pages[j]
					}
				}
				retainedMu.Unlock()
			}
		})
	}
schedule:
	for i := range slots {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break schedule
		}
	}
	close(jobs)
	wg.Wait()
	if budgetExceeded {
		return nil, metadb.ErrInvalidArgument
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeMessageUpdateReply(body []byte) (messageUpdateReadReply, error) {
	var out messageUpdateReadReply
	if len(body) > 2*metadb.MaxMessageUpdatePageBytes {
		return out, metadb.ErrInvalidArgument
	}
	err := json.Unmarshal(body, &out)
	if err == nil && out.Format != 1 {
		err = metadb.ErrInvalidArgument
	}
	return out, err
}

func (s *Store) handleMessageUpdateReadRPC(ctx context.Context, body []byte) ([]byte, error) {
	if len(body) > 256<<10 {
		return nil, metadb.ErrInvalidArgument
	}
	var req messageUpdateReadRPC
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	if req.Format == 1 && req.Probe && len(req.Reads) == 0 {
		return json.Marshal(messageUpdateReadReply{Format: 1, Status: rpcStatusOK})
	}
	out, err := s.readMessageUpdatesLocal(ctx, req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func (s *Store) readMessageUpdatesLocal(ctx context.Context, req messageUpdateReadRPC) (_ messageUpdateReadReply, readErr error) {
	out := messageUpdateReadReply{Format: 1}
	if req.Format != 1 || len(req.Reads) == 0 || len(req.Reads) > metadb.MaxMessageUpdatePage {
		return out, metadb.ErrInvalidArgument
	}
	slot := multiraft.SlotID(req.SlotID)
	targets := 0
	for _, q := range req.Reads {
		targets += max(1, max(len(q.IDs), q.Limit))
		if targets > metadb.MaxMessageUpdatePage {
			return out, metadb.ErrInvalidArgument
		}
		if q.ChannelID == "" {
			return out, metadb.ErrInvalidArgument
		}
		if s.cluster.SlotForKey(q.ChannelID) != slot {
			return out, ErrReadStaleRoute
		}
	}
	revision := s.cluster.HashSlotTableVersion()
	leader, err := s.cluster.LeaderOf(slot)
	if err != nil {
		out.Status = rpcStatusNoLeader
		return out, nil
	}
	if !s.cluster.IsLocal(leader) {
		out.Status = rpcStatusNotLeader
		out.LeaderID = uint64(leader)
		return out, nil
	}
	barrierStart := s.startMessageUpdateStage()
	// Production uses a local-only ReadIndex barrier. Narrow test/embedding
	// ports without it retain the conservative fresh-noop compatibility path.
	if reader, ok := s.cluster.(interface {
		ReadSlotBarrier(context.Context, multiraft.SlotID) error
	}); ok {
		err = reader.ReadSlotBarrier(ctx, slot)
	} else {
		hs := hashSlotForKey(s.cluster, req.Reads[0].ChannelID)
		err = proposeLocalWithHashSlot(ctx, s.cluster, slot, hs, metafsm.EncodeNoopCommand())
	}
	s.finishMessageUpdateStage("barrier", barrierStart, err)
	if err != nil {
		return out, err
	}
	storageStart := s.startMessageUpdateStage()
	defer func() { s.finishMessageUpdateStage("storage", storageStart, readErr) }()
	hashSlots := make([]uint16, len(req.Reads))
	for i, q := range req.Reads {
		if s.cluster.SlotForKey(q.ChannelID) != slot {
			return out, ErrReadStaleRoute
		}
		hashSlots[i] = hashSlotForKey(s.cluster, q.ChannelID)
	}
	// All logical shards share this DB. One pinned view after the fresh Slot
	// barrier avoids per-channel snapshot bookkeeping without caching any proof.
	out.Pages, err = s.db.ReadMessageUpdatesBatch(ctx, hashSlots, req.Reads)
	if err != nil {
		return out, err
	}
	now, e := s.cluster.LeaderOf(slot)
	if e != nil || now != leader || !s.cluster.IsLocal(now) || revision != s.cluster.HashSlotTableVersion() {
		return messageUpdateReadReply{}, ErrReadStaleRoute
	}
	for _, q := range req.Reads {
		if s.cluster.SlotForKey(q.ChannelID) != slot {
			return messageUpdateReadReply{}, ErrReadStaleRoute
		}
	}
	out.Status = rpcStatusOK
	return out, nil
}

// checkMessageUpdateReplicas fails closed before introducing the new command
// into a newly activated replica set containing an old or unreachable replica.
// The replicated head retains the proof, so later quorum writes remain available
// when an already-activated replica is offline. Upgrades still require
// a coordinated binary rollout; changing voters during feature activation is unsupported.
func (s *Store) checkMessageUpdateReplicas(ctx context.Context, key string, known string) (string, error) {
	slot := s.cluster.SlotForKey(key)
	revision := s.cluster.HashSlotTableVersion()
	peers := append([]multiraft.NodeID(nil), s.cluster.PeersForSlot(slot)...)
	if len(peers) == 0 || len(peers) > 256 {
		return "", metadb.ErrStaleMeta
	}
	slices.Sort(peers)
	encoded, _ := json.Marshal(peers)
	proof := string(encoded)
	if proof == known {
		return proof, nil
	}
	body, _ := json.Marshal(messageUpdateReadRPC{Format: 1, Probe: true, SlotID: uint64(slot)})
	for _, peer := range peers {
		if s.cluster.IsLocal(peer) {
			continue
		}
		raw, err := s.cluster.RPCService(ctx, peer, slot, messageUpdateRPCServiceID, body)
		if err != nil {
			return "", fmt.Errorf("message updates require compatible Slot replicas: %w", err)
		}
		reply, err := decodeMessageUpdateReply(raw)
		if err != nil || reply.Status != rpcStatusOK {
			return "", metadb.ErrStaleMeta
		}
	}
	current := append([]multiraft.NodeID(nil), s.cluster.PeersForSlot(slot)...)
	slices.Sort(current)
	if s.cluster.SlotForKey(key) != slot || revision != s.cluster.HashSlotTableVersion() || !slices.Equal(peers, current) {
		return "", metadb.ErrStaleMeta
	}
	return proof, nil
}
