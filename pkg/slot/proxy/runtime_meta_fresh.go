package proxy

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func validRuntimeMetaFreshRequest(req runtimeMetaRPCRequest) bool {
	return req.SlotID != 0 && len(req.ChannelID) > 0 && len(req.ChannelID) <= 1024 &&
		utf8.ValidString(req.ChannelID) && !strings.ContainsRune(req.ChannelID, 0) && req.ChannelType > 0 && req.ChannelType <= 255 &&
		len(req.Keys) == 0 && req.After == nil && req.Limit == 0
}

// GetChannelRuntimeMetaFresh performs a fresh Slot quorum/apply point read.
// It never creates metadata, uses a replica read or downgrades to a legacy codec.
func (s *Store) GetChannelRuntimeMetaFresh(ctx context.Context, id string, typ int64) (metadb.ChannelRuntimeMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if s == nil || s.cluster == nil {
		return metadb.ChannelRuntimeMeta{}, errSlotNotFound
	}
	revision := s.cluster.HashSlotTableVersion()
	slot, hashSlot := s.cluster.SlotForKey(id), s.cluster.HashSlotForKey(id)
	req := runtimeMetaRPCRequest{Op: runtimeMetaRPCGetFresh, CodecVersion: 3, SlotID: uint64(slot), ChannelID: id, ChannelType: typ}
	if !validRuntimeMetaFreshRequest(req) {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrInvalidArgument
	}
	var meta metadb.ChannelRuntimeMeta
	var err error
	if s.shouldServeSlotLocally(slot) {
		meta, err = s.readRuntimeMetaFreshLocal(ctx, req)
	} else {
		var body []byte
		body, err = encodeRuntimeMetaRPCRequestBinary(req)
		if err == nil {
			var reply runtimeMetaRPCResponse
			reply, err = callAuthoritativeRPC(ctx, s, slot, runtimeMetaRPCServiceID, body, func(b []byte) (runtimeMetaRPCResponse, error) {
				version, ok := runtimeMetaRPCResponseVersion(b)
				if !ok || version != 3 || len(b) > 16<<10 {
					return runtimeMetaRPCResponse{}, metadb.ErrCorruptValue
				}
				response, decodeErr := decodeRuntimeMetaRPCResponse(b)
				if decodeErr != nil {
					return runtimeMetaRPCResponse{}, decodeErr
				}
				if len(response.Metas) != 0 || response.Cursor != (metadb.ChannelRuntimeMetaCursor{}) || response.Done {
					return runtimeMetaRPCResponse{}, metadb.ErrCorruptValue
				}
				return response, nil
			})
			if err == nil {
				switch {
				case reply.Status == rpcStatusNotFound && reply.Meta == nil:
					err = metadb.ErrNotFound
				case reply.Status == rpcStatusOK && reply.Meta != nil:
					meta = *reply.Meta
				default:
					err = metadb.ErrCorruptValue
				}
			}
		}
	}
	if revision != s.cluster.HashSlotTableVersion() || slot != s.cluster.SlotForKey(id) || hashSlot != s.cluster.HashSlotForKey(id) {
		return metadb.ChannelRuntimeMeta{}, ErrReadStaleRoute
	}
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if meta.ChannelID != id || meta.ChannelType != typ {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrCorruptValue
	}
	if err := ctx.Err(); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	return meta, nil
}

// readRuntimeMetaFreshLocal fences both found and absent rows with fresh local
// leadership and applied progress. A barrier cannot be replaced by forwarding.
func (s *Store) readRuntimeMetaFreshLocal(ctx context.Context, req runtimeMetaRPCRequest) (metadb.ChannelRuntimeMeta, error) {
	if !validRuntimeMetaFreshRequest(req) || req.CodecVersion != 3 {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrInvalidArgument
	}
	if s == nil || s.cluster == nil {
		return metadb.ChannelRuntimeMeta{}, errSlotNotFound
	}
	if err := ctx.Err(); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	slot := multiraft.SlotID(req.SlotID)
	revision, hashSlot := s.cluster.HashSlotTableVersion(), s.cluster.HashSlotForKey(req.ChannelID)
	if s.cluster.SlotForKey(req.ChannelID) != slot {
		return metadb.ChannelRuntimeMeta{}, ErrReadStaleRoute
	}
	leader, err := s.cluster.LeaderOf(slot)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if leader == 0 || !s.cluster.IsLocal(leader) {
		return metadb.ChannelRuntimeMeta{}, ErrNotLeader
	}
	if reader, ok := s.cluster.(interface {
		ReadSlotBarrier(context.Context, multiraft.SlotID) error
	}); ok {
		err = reader.ReadSlotBarrier(ctx, slot)
	} else {
		err = proposeLocalWithHashSlot(ctx, s.cluster, slot, hashSlot, metafsm.EncodeNoopCommand())
	}
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	meta, readErr := s.db.ForHashSlot(hashSlot).GetChannelRuntimeMeta(ctx, req.ChannelID, req.ChannelType)
	if readErr != nil && !errors.Is(readErr, metadb.ErrNotFound) {
		return metadb.ChannelRuntimeMeta{}, readErr
	}
	current, err := s.cluster.LeaderOf(slot)
	if err != nil || current != leader || !s.cluster.IsLocal(current) || revision != s.cluster.HashSlotTableVersion() ||
		slot != s.cluster.SlotForKey(req.ChannelID) || hashSlot != s.cluster.HashSlotForKey(req.ChannelID) {
		return metadb.ChannelRuntimeMeta{}, ErrReadStaleRoute
	}
	if err := ctx.Err(); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	return meta, readErr
}
