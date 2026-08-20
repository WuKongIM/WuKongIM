package proxy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	permissionBatchRPCServiceID = clusternet.RPCSlotPermissionMetadataBatch
	permissionBatchMaxReads     = 4096
	// The reviewed cluster topology has twelve physical Slots. Covering one
	// request's represented Slots in a single bounded wave avoids making a
	// mixed gateway batch pay serial authoritative-RPC rounds.
	permissionBatchSlotWorkers = 12
)

var (
	permissionBatchRequestMagic  = [...]byte{'W', 'K', 'P', 'Q', 1}
	permissionBatchResponseMagic = [...]byte{'W', 'K', 'P', 'S', 1}
)

// PermissionMetadataReadKind identifies one raw permission fact.
type PermissionMetadataReadKind uint8

const (
	PermissionMetadataReadChannel PermissionMetadataReadKind = iota + 1
	PermissionMetadataReadSubscriberContains
	PermissionMetadataReadSubscriberHasAny
)

// PermissionMetadataRead is one channel-owned authoritative lookup.
type PermissionMetadataRead struct {
	Kind        PermissionMetadataReadKind
	ChannelID   string
	ChannelType int64
	UID         string
}

// PermissionMetadataReadResult aligns with one PermissionMetadataRead.
type PermissionMetadataReadResult struct {
	Channel metadb.Channel
	Found   bool
	Value   bool
	Err     error
}

type indexedPermissionMetadataRead struct {
	index int
	read  PermissionMetadataRead
}

type permissionMetadataSlotGroup struct {
	slotID multiraft.SlotID
	items  []indexedPermissionMetadataRead
}

type permissionBatchRPCRequest struct {
	SlotID uint64
	Reads  []PermissionMetadataRead
}

type permissionBatchRPCResponse struct {
	Status   string
	LeaderID uint64
	Results  []PermissionMetadataReadResult
}

func (r permissionBatchRPCResponse) rpcStatus() string { return r.Status }

func (r permissionBatchRPCResponse) rpcLeaderID() uint64 { return r.LeaderID }

// ReadPermissionMetadataBatch groups raw facts by physical Slot and performs
// at most one authoritative RPC per represented Slot. Results preserve input
// alignment, and a Slot-local failure affects only reads owned by that Slot.
func (s *Store) ReadPermissionMetadataBatch(ctx context.Context, reads []PermissionMetadataRead) []PermissionMetadataReadResult {
	results := make([]PermissionMetadataReadResult, len(reads))
	if len(reads) == 0 {
		return results
	}
	if s == nil || s.cluster == nil || s.db == nil {
		return permissionMetadataErrorResults(results, fmt.Errorf("metastore: permission batch store not ready"))
	}
	if len(reads) > permissionBatchMaxReads {
		return permissionMetadataErrorResults(results, fmt.Errorf("metastore: permission batch has %d reads, max %d", len(reads), permissionBatchMaxReads))
	}

	grouped := make(map[multiraft.SlotID][]indexedPermissionMetadataRead)
	for index, read := range reads {
		if read.ChannelID == "" {
			results[index].Err = metadb.ErrInvalidArgument
			continue
		}
		slotID := s.cluster.SlotForKey(read.ChannelID)
		if slotID == 0 {
			results[index].Err = errSlotNotFound
			continue
		}
		grouped[slotID] = append(grouped[slotID], indexedPermissionMetadataRead{index: index, read: read})
	}
	groups := make([]permissionMetadataSlotGroup, 0, len(grouped))
	for slotID, items := range grouped {
		groups = append(groups, permissionMetadataSlotGroup{slotID: slotID, items: items})
	}
	runPermissionMetadataSlotWorkers(len(groups), func(groupIndex int) {
		group := groups[groupIndex]
		groupReads := make([]PermissionMetadataRead, len(group.items))
		for i := range group.items {
			groupReads[i] = group.items[i].read
		}
		groupResults, err := s.readPermissionMetadataGroup(ctx, group.slotID, groupReads)
		if err != nil {
			for _, item := range group.items {
				results[item.index].Err = err
			}
			return
		}
		if len(groupResults) != len(group.items) {
			err := fmt.Errorf("metastore: permission batch returned %d results for %d reads", len(groupResults), len(group.items))
			for _, item := range group.items {
				results[item.index].Err = err
			}
			return
		}
		for i, item := range group.items {
			results[item.index] = groupResults[i]
		}
	})
	return results
}

func runPermissionMetadataSlotWorkers(groupCount int, run func(int)) {
	workers := min(groupCount, permissionBatchSlotWorkers)
	if workers <= 1 {
		for i := 0; i < groupCount; i++ {
			run(i)
		}
		return
	}
	var next atomic.Uint64
	worker := func() {
		for {
			index := int(next.Add(1) - 1)
			if index >= groupCount {
				return
			}
			run(index)
		}
	}
	var wait sync.WaitGroup
	wait.Add(workers - 1)
	for range workers - 1 {
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskSlotPermissionBatch, func() {
			defer wait.Done()
			worker()
		})
	}
	worker()
	wait.Wait()
}

func permissionMetadataErrorResults(results []PermissionMetadataReadResult, err error) []PermissionMetadataReadResult {
	for i := range results {
		results[i].Err = err
	}
	return results
}

func (s *Store) readPermissionMetadataGroup(ctx context.Context, slotID multiraft.SlotID, reads []PermissionMetadataRead) ([]PermissionMetadataReadResult, error) {
	if s.shouldServeSlotLocally(slotID) {
		return s.readPermissionMetadataLocal(ctx, slotID, reads)
	}
	payload, err := encodePermissionBatchRPCRequest(permissionBatchRPCRequest{SlotID: uint64(slotID), Reads: reads})
	if err != nil {
		return nil, err
	}
	resp, err := callAuthoritativeRPC(ctx, s, slotID, permissionBatchRPCServiceID, payload, decodePermissionBatchRPCResponse)
	if err != nil {
		return nil, err
	}
	return resp.Results, nil
}

func (s *Store) handlePermissionBatchRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodePermissionBatchRPCRequest(body)
	if err != nil {
		return nil, err
	}
	slotID := multiraft.SlotID(req.SlotID)
	if statusBody, handled, err := s.handleAuthoritativeRPC(slotID, func(status string, leaderID uint64) ([]byte, error) {
		return encodePermissionBatchRPCResponse(permissionBatchRPCResponse{Status: status, LeaderID: leaderID})
	}); handled || err != nil {
		return statusBody, err
	}
	results, err := s.readPermissionMetadataLocal(ctx, slotID, req.Reads)
	if err != nil {
		return nil, err
	}
	return encodePermissionBatchRPCResponse(permissionBatchRPCResponse{Status: rpcStatusOK, Results: results})
}

func (s *Store) readPermissionMetadataLocal(ctx context.Context, slotID multiraft.SlotID, reads []PermissionMetadataRead) ([]PermissionMetadataReadResult, error) {
	results := make([]PermissionMetadataReadResult, len(reads))
	for i, read := range reads {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if read.ChannelID == "" || s.cluster.SlotForKey(read.ChannelID) != slotID {
			return nil, fmt.Errorf("metastore: permission read %d is not owned by slot %d", i, slotID)
		}
		hashSlot := hashSlotForKey(s.cluster, read.ChannelID)
		shard := s.db.ForHashSlot(hashSlot)
		switch read.Kind {
		case PermissionMetadataReadChannel:
			channel, err := shard.GetChannel(ctx, read.ChannelID, read.ChannelType)
			if errors.Is(err, metadb.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			results[i].Channel = channel
			results[i].Found = true
		case PermissionMetadataReadSubscriberContains:
			value, err := shard.ContainsSubscriber(ctx, read.ChannelID, read.ChannelType, read.UID)
			if err != nil {
				return nil, err
			}
			results[i].Value = value
		case PermissionMetadataReadSubscriberHasAny:
			value, err := shard.HasSubscribers(ctx, read.ChannelID, read.ChannelType)
			if err != nil {
				return nil, err
			}
			results[i].Value = value
		default:
			return nil, fmt.Errorf("metastore: unknown permission read kind %d", read.Kind)
		}
	}
	return results, nil
}

func encodePermissionBatchRPCRequest(req permissionBatchRPCRequest) ([]byte, error) {
	if len(req.Reads) > permissionBatchMaxReads {
		return nil, fmt.Errorf("metastore: permission batch has %d reads, max %d", len(req.Reads), permissionBatchMaxReads)
	}
	dst := make([]byte, 0, len(permissionBatchRequestMagic)+len(req.Reads)*32)
	dst = append(dst, permissionBatchRequestMagic[:]...)
	dst = runtimeMetaAppendUvarint(dst, req.SlotID)
	dst = runtimeMetaAppendUvarint(dst, uint64(len(req.Reads)))
	for _, read := range req.Reads {
		dst = append(dst, byte(read.Kind))
		dst = runtimeMetaAppendString(dst, read.ChannelID)
		dst = runtimeMetaAppendVarint(dst, read.ChannelType)
		dst = runtimeMetaAppendString(dst, read.UID)
	}
	return dst, nil
}

func decodePermissionBatchRPCRequest(body []byte) (permissionBatchRPCRequest, error) {
	if !runtimeMetaHasMagic(body, permissionBatchRequestMagic[:]) {
		return permissionBatchRPCRequest{}, fmt.Errorf("metastore: invalid permission batch request codec")
	}
	offset := len(permissionBatchRequestMagic)
	var req permissionBatchRPCRequest
	var err error
	if req.SlotID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return permissionBatchRPCRequest{}, err
	}
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return permissionBatchRPCRequest{}, err
	}
	offset = next
	if count > permissionBatchMaxReads {
		return permissionBatchRPCRequest{}, fmt.Errorf("metastore: permission batch has %d reads, max %d", count, permissionBatchMaxReads)
	}
	readCount, err := runtimeMetaCollectionLen(count, len(body)-offset, "permission batch reads")
	if err != nil {
		return permissionBatchRPCRequest{}, err
	}
	req.Reads = make([]PermissionMetadataRead, readCount)
	for i := range req.Reads {
		if offset >= len(body) {
			return permissionBatchRPCRequest{}, fmt.Errorf("metastore: short permission batch read kind")
		}
		req.Reads[i].Kind = PermissionMetadataReadKind(body[offset])
		offset++
		if req.Reads[i].ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return permissionBatchRPCRequest{}, err
		}
		if req.Reads[i].ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return permissionBatchRPCRequest{}, err
		}
		if req.Reads[i].UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return permissionBatchRPCRequest{}, err
		}
	}
	if offset != len(body) {
		return permissionBatchRPCRequest{}, fmt.Errorf("metastore: trailing permission batch request bytes")
	}
	return req, nil
}

func encodePermissionBatchRPCResponse(resp permissionBatchRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, len(permissionBatchResponseMagic)+len(resp.Results)*32)
	dst = append(dst, permissionBatchResponseMagic[:]...)
	dst = runtimeMetaAppendString(dst, resp.Status)
	dst = runtimeMetaAppendUvarint(dst, resp.LeaderID)
	dst = runtimeMetaAppendUvarint(dst, uint64(len(resp.Results)))
	for _, result := range resp.Results {
		if result.Err != nil {
			return nil, fmt.Errorf("metastore: permission batch response contains local error: %w", result.Err)
		}
		if result.Found {
			channel := result.Channel
			dst = appendChannelPtr(dst, &channel)
		} else {
			dst = appendChannelPtr(dst, nil)
		}
		dst = runtimeMetaAppendBool(dst, result.Value)
	}
	return dst, nil
}

func decodePermissionBatchRPCResponse(body []byte) (permissionBatchRPCResponse, error) {
	if !runtimeMetaHasMagic(body, permissionBatchResponseMagic[:]) {
		return permissionBatchRPCResponse{}, fmt.Errorf("metastore: invalid permission batch response codec")
	}
	offset := len(permissionBatchResponseMagic)
	var resp permissionBatchRPCResponse
	var err error
	if resp.Status, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return permissionBatchRPCResponse{}, err
	}
	if resp.LeaderID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return permissionBatchRPCResponse{}, err
	}
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return permissionBatchRPCResponse{}, err
	}
	offset = next
	if count > permissionBatchMaxReads {
		return permissionBatchRPCResponse{}, fmt.Errorf("metastore: permission batch response has %d results, max %d", count, permissionBatchMaxReads)
	}
	resultCount, err := runtimeMetaCollectionLen(count, len(body)-offset, "permission batch results")
	if err != nil {
		return permissionBatchRPCResponse{}, err
	}
	resp.Results = make([]PermissionMetadataReadResult, resultCount)
	for i := range resp.Results {
		channel, next, err := readChannelPtr(body, offset)
		if err != nil {
			return permissionBatchRPCResponse{}, err
		}
		offset = next
		if channel != nil {
			resp.Results[i].Channel = *channel
			resp.Results[i].Found = true
		}
		if resp.Results[i].Value, offset, err = runtimeMetaReadBool(body, offset); err != nil {
			return permissionBatchRPCResponse{}, err
		}
	}
	if offset != len(body) {
		return permissionBatchRPCResponse{}, fmt.Errorf("metastore: trailing permission batch response bytes")
	}
	return resp, nil
}
