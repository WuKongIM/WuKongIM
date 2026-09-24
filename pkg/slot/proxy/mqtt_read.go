package proxy

import (
	"context"
	"encoding/json"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	mqttReadRPCServiceID  = clusternet.RPCSlotMQTTMetadata
	mqttReadRequestBytes  = 64 << 10
	mqttReadResponseBytes = 8 << 20
)

type mqttReadRPC struct {
	Format   uint8           `json:"format"`
	SlotID   uint64          `json:"slot_id"`
	HashSlot uint16          `json:"hash_slot"`
	Query    metadb.MQTTRead `json:"query"`
	Probe    bool            `json:"probe,omitempty"`
}

type mqttReadReply struct {
	Format   uint8           `json:"format"`
	SlotID   uint64          `json:"slot_id"`
	HashSlot uint16          `json:"hash_slot"`
	Query    metadb.MQTTRead `json:"query"`
	Status   string          `json:"status"`
	LeaderID uint64          `json:"leader_id,omitempty"`
	// A present result distinguishes authoritative absence from a missing reply.
	Result *metadb.MQTTReadResult `json:"result,omitempty"`
}

func (r mqttReadReply) rpcStatus() string   { return r.Status }
func (r mqttReadReply) rpcLeaderID() uint64 { return r.LeaderID }

// ReadMQTT obtains a bounded entity view at its current Slot authority. Each
// request establishes a fresh apply barrier and one pinned database snapshot.
func (s *Store) ReadMQTT(ctx context.Context, q metadb.MQTTRead) (metadb.MQTTReadResult, error) {
	if err := metadb.ValidateMQTTRead(q); err != nil {
		return metadb.MQTTReadResult{}, err
	}
	key, err := mqttReadRoutingKey(q)
	if err != nil {
		return metadb.MQTTReadResult{}, err
	}
	if s == nil || s.cluster == nil {
		return metadb.MQTTReadResult{}, errSlotNotFound
	}
	revision := s.cluster.HashSlotTableVersion()
	req := mqttReadRPC{Format: 1, SlotID: uint64(s.cluster.SlotForKey(key)), HashSlot: s.cluster.HashSlotForKey(key), Query: q}
	return s.readMQTT(ctx, req, revision)
}

// ReadMQTTRecovery scans one logical hash Slot, never a node-local collection.
// Candidates must be revalidated before executing lifecycle or retention work.
func (s *Store) ReadMQTTRecovery(ctx context.Context, hashSlot uint16, q metadb.MQTTRead) (metadb.MQTTReadResult, error) {
	if err := metadb.ValidateMQTTRead(q); err != nil {
		return metadb.MQTTReadResult{}, err
	}
	if !q.Recovery() {
		return metadb.MQTTReadResult{}, metadb.ErrInvalidArgument
	}
	if s == nil || s.cluster == nil {
		return metadb.MQTTReadResult{}, errSlotNotFound
	}
	revision := s.cluster.HashSlotTableVersion()
	slot, err := s.mqttHashSlotOwner(hashSlot)
	if err != nil {
		return metadb.MQTTReadResult{}, err
	}
	return s.readMQTT(ctx, mqttReadRPC{Format: 1, SlotID: uint64(slot), HashSlot: hashSlot, Query: q}, revision)
}

func (s *Store) readMQTT(ctx context.Context, req mqttReadRPC, revision uint64) (metadb.MQTTReadResult, error) {
	if err := ctx.Err(); err != nil {
		return metadb.MQTTReadResult{}, err
	}
	slot := multiraft.SlotID(req.SlotID)
	if revision != s.cluster.HashSlotTableVersion() || !s.mqttRouteMatches(slot, req.HashSlot, req.Query) {
		return metadb.MQTTReadResult{}, ErrReadStaleRoute
	}
	var reply mqttReadReply
	var err error
	if s.shouldServeSlotLocally(slot) {
		reply, err = s.readMQTTLocal(ctx, req)
		if err == nil && reply.Status != rpcStatusOK {
			return metadb.MQTTReadResult{}, ErrReadStaleRoute
		}
	} else {
		body, e := json.Marshal(req)
		if e != nil {
			return metadb.MQTTReadResult{}, e
		}
		if len(body) > mqttReadRequestBytes {
			return metadb.MQTTReadResult{}, metadb.ErrInvalidArgument
		}
		reply, err = callAuthoritativeRPC(ctx, s, slot, mqttReadRPCServiceID, body, func(b []byte) (mqttReadReply, error) { return decodeMQTTReadReply(b, req) })
	}
	if err != nil {
		return metadb.MQTTReadResult{}, err
	}
	if revision != s.cluster.HashSlotTableVersion() || !s.mqttRouteMatches(slot, req.HashSlot, req.Query) {
		return metadb.MQTTReadResult{}, ErrReadStaleRoute
	}
	if reply.Result == nil {
		return metadb.MQTTReadResult{}, metadb.ErrCorruptValue
	}
	return *reply.Result, nil
}

func (s *Store) handleMQTTReadRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req mqttReadRPC
	if err := decodeMQTTJSON(body, mqttReadRequestBytes, &req); err != nil {
		return nil, err
	}
	if req.Format != 1 {
		return nil, metadb.ErrInvalidArgument
	}
	if req.Probe {
		if req.SlotID != 0 || req.HashSlot != 0 || req.Query != (metadb.MQTTRead{}) {
			return nil, metadb.ErrInvalidArgument
		}
		return json.Marshal(mqttReadReply{Format: 1, Status: rpcStatusOK})
	}
	reply, err := s.readMQTTLocal(ctx, req)
	if err != nil {
		return nil, err
	}
	body, err = json.Marshal(reply)
	if len(body) > mqttReadResponseBytes {
		return nil, metadb.ErrInvalidArgument
	}
	return body, err
}

func (s *Store) readMQTTLocal(ctx context.Context, req mqttReadRPC) (mqttReadReply, error) {
	out := mqttReadReply{Format: 1, SlotID: req.SlotID, HashSlot: req.HashSlot, Query: req.Query}
	if req.Format != 1 || req.Probe {
		return out, metadb.ErrInvalidArgument
	}
	if err := metadb.ValidateMQTTRead(req.Query); err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if s == nil || s.cluster == nil {
		return out, errSlotNotFound
	}
	revision := s.cluster.HashSlotTableVersion()
	slot := multiraft.SlotID(req.SlotID)
	if !s.mqttRouteMatches(slot, req.HashSlot, req.Query) {
		return out, ErrReadStaleRoute
	}
	leader, err := s.cluster.LeaderOf(slot)
	if err != nil || leader == 0 {
		out.Status = rpcStatusNoLeader
		if isSlotNotFound(err) {
			out.Status = rpcStatusNoSlot
		}
		return out, nil
	}
	if !s.cluster.IsLocal(leader) {
		out.Status, out.LeaderID = rpcStatusNotLeader, uint64(leader)
		return out, nil
	}
	// Production uses ReadIndex plus durable apply; an embedding without that
	// port must commit a fresh local-only noop, never a forwarded proposal.
	if reader, ok := s.cluster.(interface {
		ReadSlotBarrier(context.Context, multiraft.SlotID) error
	}); ok {
		err = reader.ReadSlotBarrier(ctx, slot)
	} else {
		err = proposeLocalWithHashSlot(ctx, s.cluster, slot, req.HashSlot, metafsm.EncodeNoopCommand())
	}
	if err != nil {
		return out, err
	}
	result, err := s.db.ReadMQTTState(ctx, req.HashSlot, req.Query)
	if err != nil {
		return out, err
	}
	now, err := s.cluster.LeaderOf(slot)
	if err != nil || now != leader || !s.cluster.IsLocal(now) || revision != s.cluster.HashSlotTableVersion() || !s.mqttRouteMatches(slot, req.HashSlot, req.Query) {
		return out, ErrReadStaleRoute
	}
	out.Status, out.Result = rpcStatusOK, &result
	return out, nil
}

func decodeMQTTReadReply(body []byte, req mqttReadRPC) (mqttReadReply, error) {
	var r mqttReadReply
	if err := decodeMQTTJSON(body, mqttReadResponseBytes, &r); err != nil {
		return r, err
	}
	if r.Format != 1 || r.SlotID != req.SlotID || r.HashSlot != req.HashSlot || r.Query != req.Query {
		return mqttReadReply{}, metadb.ErrCorruptValue
	}
	if r.Status == rpcStatusOK {
		if r.LeaderID != 0 || r.Result == nil {
			return mqttReadReply{}, metadb.ErrCorruptValue
		}
		if err := validateMQTTReadShape(req.Query, *r.Result); err != nil {
			return mqttReadReply{}, err
		}
	} else if r.Result != nil {
		return mqttReadReply{}, metadb.ErrCorruptValue
	}
	return r, nil
}

func validateMQTTReadShape(q metadb.MQTTRead, r metadb.MQTTReadResult) error {
	bad := metadb.ErrCorruptValue
	ns, client, sessionOwned := q.SessionIdentity()
	if r.Session != nil && (!sessionOwned || r.Session.Namespace != ns || r.Session.ClientID != client || metadb.ValidateMQTTSession(*r.Session) != nil) {
		return bad
	}
	counts := [7]int{len(r.Sessions), len(r.Subscriptions), len(r.DeliveryCursors), len(r.Inflight), len(r.Bindings), len(r.Wills), len(r.SourceOwners)}
	selected := -1
	switch q.Kind {
	case metadb.MQTTReadSessionDeadlines:
		selected = 0
	case metadb.MQTTReadSubscription, metadb.MQTTReadSubscriptions, metadb.MQTTReadSubscriptionRecovery:
		selected = 1
	case metadb.MQTTReadDeliveryCursor, metadb.MQTTReadDeliveryCursors, metadb.MQTTReadAccounting:
		selected = 2
	case metadb.MQTTReadInflight, metadb.MQTTReadInflightPage:
		selected = 3
	case metadb.MQTTReadSourceBinding, metadb.MQTTReadSourceCandidates, metadb.MQTTReadSourceRecovery, metadb.MQTTReadSourceRetention:
		selected = 4
	case metadb.MQTTReadWill, metadb.MQTTReadWillRecovery:
		selected = 5
	case metadb.MQTTReadSourceOwners, metadb.MQTTReadReplaySources:
		selected = 6
	}
	if q.Kind == metadb.MQTTReadAccounting {
		if len(r.DeliveryCursors) > 1 || (len(r.DeliveryCursors) == 0 && r.Accounting != nil) {
			return bad
		}
		if len(r.DeliveryCursors) == 1 && metadb.ValidateMQTTAccountingHead(r.DeliveryCursors[0], r.Accounting) != nil {
			return bad
		}
	} else if r.Accounting != nil {
		return bad
	}
	count := 0
	for i, n := range counts {
		if n > 0 && i != selected {
			return bad
		}
		count += n
	}
	if count > max(1, q.Limit) || !r.Done && (q.Limit == 0 || count != q.Limit) {
		return bad
	}
	if q.Limit == 0 && r.After != q.After {
		return bad
	}
	if count == 0 && r.After != q.After {
		return bad
	}
	next := q
	next.After = r.After
	if metadb.ValidateMQTTRead(next) != nil || !r.Done && r.After == q.After {
		return bad
	}
	for _, v := range r.Sessions {
		if metadb.ValidateMQTTSession(v) != nil {
			return bad
		}
	}
	for _, v := range r.Subscriptions {
		if metadb.ValidateMQTTSubscription(v) != nil || sessionOwned && (v.Namespace != ns || v.ClientID != client || v.SessionGeneration != q.SessionGeneration) || q.Kind == metadb.MQTTReadSubscription && v.Topic != q.Topic {
			return bad
		}
	}
	for _, v := range r.DeliveryCursors {
		if metadb.ValidateMQTTDeliveryCursor(v) != nil || v.Key.Namespace != ns || v.Key.ClientID != client || (q.Kind == metadb.MQTTReadDeliveryCursor || q.Kind == metadb.MQTTReadAccounting) && v.Key != q.CursorKey || q.Kind == metadb.MQTTReadDeliveryCursors && (v.Key.SessionGeneration != q.SessionGeneration || v.Key.SubscriptionGeneration != q.SubscriptionGeneration) {
			return bad
		}
	}
	for _, v := range r.Inflight {
		if metadb.ValidateMQTTInflight(v) != nil || v.Key.Namespace != ns || v.Key.ClientID != client || v.Key.SessionGeneration != q.SessionGeneration || v.Direction != metadb.MQTTOutbound || q.Kind == metadb.MQTTReadInflight && v.PacketID != q.PacketID {
			return bad
		}
	}
	for _, v := range r.Bindings {
		if metadb.ValidateMQTTSourceBinding(v) != nil || q.Kind == metadb.MQTTReadSourceBinding && v.Key != q.BindingKey || (q.Kind == metadb.MQTTReadSourceCandidates || q.Kind == metadb.MQTTReadSourceRetention) && v.Key.Owner != q.Owner {
			return bad
		}
	}
	previous := q.After.SourceOwner
	for _, owner := range r.SourceOwners {
		probe := metadb.MQTTRead{Kind: q.Kind, Limit: 1, After: metadb.MQTTReadCursor{SourceOwner: owner}}
		if owner == (metadb.MQTTBindingOwner{}) || metadb.ValidateMQTTRead(probe) != nil || (previous != (metadb.MQTTBindingOwner{}) && metadb.CompareMQTTBindingOwners(previous, owner) >= 0) {
			return bad
		}
		previous = owner
	}
	if (q.Kind == metadb.MQTTReadSourceOwners || q.Kind == metadb.MQTTReadReplaySources) && r.After.SourceOwner != previous {
		return bad
	}
	for _, v := range r.Wills {
		if metadb.ValidateMQTTWill(v) != nil || q.Kind == metadb.MQTTReadWill && v.Key != q.WillKey {
			return bad
		}
	}
	return nil
}
