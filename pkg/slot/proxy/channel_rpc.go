package proxy

import (
	"container/heap"
	"context"
	"errors"
	"fmt"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const channelRPCServiceID uint8 = 12

const (
	channelRPCGetForPermission = "get_for_permission"
	channelRPCScanChannelsPage = "scan_channels_page"
)

var (
	channelRPCRequestMagicV1  = [...]byte{'W', 'K', 'C', 'Q', 1}
	channelRPCRequestMagic    = [...]byte{'W', 'K', 'C', 'Q', 2}
	channelRPCResponseMagicV1 = [...]byte{'W', 'K', 'C', 'S', 1}
	channelRPCResponseMagicV2 = [...]byte{'W', 'K', 'C', 'S', 2}
	channelRPCResponseMagic   = [...]byte{'W', 'K', 'C', 'S', 3}
)

type channelRPCRequest struct {
	Op          string `json:"op"`
	SlotID      uint64 `json:"slot_id"`
	HashSlot    uint16 `json:"hash_slot,omitempty"`
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
	After       metadb.ChannelCursor
	Limit       int `json:"limit,omitempty"`
}

type channelRPCResponse struct {
	Status   string               `json:"status"`
	LeaderID uint64               `json:"leader_id,omitempty"`
	Channel  *metadb.Channel      `json:"channel,omitempty"`
	Channels []metadb.Channel     `json:"channels,omitempty"`
	Cursor   metadb.ChannelCursor `json:"cursor,omitempty"`
	Done     bool                 `json:"done,omitempty"`
}

func (r channelRPCResponse) rpcStatus() string {
	return r.Status
}

func (r channelRPCResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (s *Store) getChannelForPermissionAuthoritative(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, channelID string, channelType int64) (metadb.Channel, error) {
	resp, err := s.callChannelRPC(ctx, slotID, channelRPCRequest{
		Op:          channelRPCGetForPermission,
		SlotID:      uint64(slotID),
		HashSlot:    hashSlot,
		ChannelID:   channelID,
		ChannelType: channelType,
	})
	if err != nil {
		return metadb.Channel{}, err
	}
	if resp.Channel == nil {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return *resp.Channel, nil
}

func (s *Store) scanChannelsSlotPageAuthoritative(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelCursor, limit int) ([]metadb.Channel, metadb.ChannelCursor, bool, error) {
	if s.shouldServeSlotLocally(slotID) {
		return s.scanChannelsSlotPageLocal(ctx, slotID, after, limit)
	}

	resp, err := s.callChannelRPC(ctx, slotID, channelRPCRequest{
		Op:     channelRPCScanChannelsPage,
		SlotID: uint64(slotID),
		After:  after,
		Limit:  limit,
	})
	if err != nil {
		return nil, metadb.ChannelCursor{}, false, err
	}
	return append([]metadb.Channel(nil), resp.Channels...), resp.Cursor, resp.Done, nil
}

func (s *Store) callChannelRPC(ctx context.Context, slotID multiraft.SlotID, req channelRPCRequest) (channelRPCResponse, error) {
	payload, err := encodeChannelRPCRequestBinary(req)
	if err != nil {
		return channelRPCResponse{}, err
	}
	return callAuthoritativeRPC(ctx, s, slotID, channelRPCServiceID, payload, decodeChannelRPCResponse)
}

func (s *Store) handleChannelRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeChannelRPCRequest(body)
	if err != nil {
		return nil, err
	}

	slotID := multiraft.SlotID(req.SlotID)
	if statusBody, handled, err := s.handleAuthoritativeRPC(slotID, func(status string, leaderID uint64) ([]byte, error) {
		return encodeChannelRPCResponse(channelRPCResponse{
			Status:   status,
			LeaderID: leaderID,
		})
	}); handled || err != nil {
		return statusBody, err
	}

	switch channelRPCOpOrDefault(req.Op) {
	case channelRPCGetForPermission:
		hashSlot := req.HashSlot
		if hashSlot == 0 {
			hashSlot = hashSlotForKey(s.cluster, req.ChannelID)
		}
		ch, err := s.db.ForHashSlot(hashSlot).GetChannel(ctx, req.ChannelID, req.ChannelType)
		if errors.Is(err, metadb.ErrNotFound) {
			return encodeChannelRPCResponse(channelRPCResponse{Status: rpcStatusNotFound})
		}
		if err != nil {
			return nil, err
		}
		return encodeChannelRPCResponse(channelRPCResponse{Status: rpcStatusOK, Channel: &ch})
	case channelRPCScanChannelsPage:
		channels, cursor, done, err := s.scanChannelsSlotPageLocal(ctx, slotID, req.After, req.Limit)
		if err != nil {
			return nil, err
		}
		return encodeChannelRPCResponse(channelRPCResponse{
			Status:   rpcStatusOK,
			Channels: channels,
			Cursor:   cursor,
			Done:     done,
		})
	default:
		return nil, fmt.Errorf("metastore: unknown channel rpc op %q", req.Op)
	}
}

func (s *Store) scanChannelsSlotPageLocal(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelCursor, limit int) ([]metadb.Channel, metadb.ChannelCursor, bool, error) {
	if s.cluster == nil {
		return nil, metadb.ChannelCursor{}, false, fmt.Errorf("metastore: cluster not configured")
	}
	if limit <= 0 {
		return nil, metadb.ChannelCursor{}, false, metadb.ErrInvalidArgument
	}

	hashSlots := s.cluster.HashSlotsOf(slotID)
	if len(hashSlots) == 0 {
		return nil, metadb.ChannelCursor{}, false, raftcluster.ErrSlotNotFound
	}

	queue := make(channelMergeHeap, 0, len(hashSlots))
	for _, hashSlot := range hashSlots {
		item, ok, err := s.loadChannelMergeItem(ctx, hashSlot, after)
		if err != nil {
			return nil, metadb.ChannelCursor{}, false, err
		}
		if ok {
			heap.Push(&queue, item)
		}
	}

	channels := make([]metadb.Channel, 0, limit)
	cursor := after
	for len(channels) < limit && queue.Len() > 0 {
		item := heap.Pop(&queue).(channelMergeItem)
		channels = append(channels, item.Channel)
		cursor = metadb.ChannelCursor{
			ChannelID:   item.Channel.ChannelID,
			ChannelType: item.Channel.ChannelType,
		}

		if item.Done {
			continue
		}

		nextItem, ok, err := s.loadChannelMergeItem(ctx, item.HashSlot, item.Cursor)
		if err != nil {
			return nil, metadb.ChannelCursor{}, false, err
		}
		if ok {
			heap.Push(&queue, nextItem)
		}
	}

	if len(channels) == 0 {
		cursor = after
	}
	return channels, cursor, queue.Len() == 0, nil
}

func (s *Store) loadChannelMergeItem(ctx context.Context, hashSlot uint16, after metadb.ChannelCursor) (channelMergeItem, bool, error) {
	channels, cursor, done, err := s.db.ForHashSlot(hashSlot).ListChannelsPage(ctx, after, 1)
	if err != nil {
		return channelMergeItem{}, false, err
	}
	if len(channels) == 0 {
		return channelMergeItem{}, false, nil
	}
	return channelMergeItem{
		HashSlot: hashSlot,
		Channel:  channels[0],
		Cursor:   cursor,
		Done:     done,
	}, true, nil
}

func encodeChannelRPCRequestBinary(req channelRPCRequest) ([]byte, error) {
	opID, err := channelRPCOpID(channelRPCOpOrDefault(req.Op))
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(channelRPCRequestMagic)+len(req.ChannelID)+len(req.After.ChannelID)+48)
	dst = append(dst, channelRPCRequestMagic[:]...)
	dst = append(dst, opID)
	dst = runtimeMetaAppendUvarint(dst, req.SlotID)
	dst = runtimeMetaAppendUvarint(dst, uint64(req.HashSlot))
	dst = runtimeMetaAppendString(dst, req.ChannelID)
	dst = runtimeMetaAppendVarint(dst, req.ChannelType)
	dst = runtimeMetaAppendChannelCursor(dst, req.After)
	dst = runtimeMetaAppendVarint(dst, int64(req.Limit))
	return dst, nil
}

func decodeChannelRPCRequest(body []byte) (channelRPCRequest, error) {
	if runtimeMetaHasMagic(body, channelRPCRequestMagicV1[:]) {
		return decodeChannelRPCRequestV1(body)
	}
	if !runtimeMetaHasMagic(body, channelRPCRequestMagic[:]) {
		return channelRPCRequest{}, fmt.Errorf("metastore: invalid channel request codec")
	}
	offset := len(channelRPCRequestMagic)
	if offset >= len(body) {
		return channelRPCRequest{}, fmt.Errorf("metastore: short channel op")
	}
	op, err := channelRPCOpFromID(body[offset])
	if err != nil {
		return channelRPCRequest{}, err
	}
	offset++
	var req channelRPCRequest
	req.Op = op
	if req.SlotID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return channelRPCRequest{}, err
	}
	hashSlot, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return channelRPCRequest{}, err
	}
	if hashSlot > uint64(^uint16(0)) {
		return channelRPCRequest{}, fmt.Errorf("metastore: channel hash slot overflows uint16")
	}
	req.HashSlot = uint16(hashSlot)
	offset = next
	if req.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return channelRPCRequest{}, err
	}
	if req.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return channelRPCRequest{}, err
	}
	if req.After, offset, err = runtimeMetaReadChannelCursor(body, offset); err != nil {
		return channelRPCRequest{}, err
	}
	if req.Limit, offset, err = runtimeMetaReadInt(body, offset, "channel scan limit"); err != nil {
		return channelRPCRequest{}, err
	}
	if offset != len(body) {
		return channelRPCRequest{}, fmt.Errorf("metastore: trailing channel request bytes")
	}
	return req, nil
}

func decodeChannelRPCRequestV1(body []byte) (channelRPCRequest, error) {
	offset := len(channelRPCRequestMagicV1)
	var req channelRPCRequest
	var err error
	req.Op = channelRPCGetForPermission
	if req.SlotID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return channelRPCRequest{}, err
	}
	hashSlot, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return channelRPCRequest{}, err
	}
	if hashSlot > uint64(^uint16(0)) {
		return channelRPCRequest{}, fmt.Errorf("metastore: channel hash slot overflows uint16")
	}
	req.HashSlot = uint16(hashSlot)
	offset = next
	if req.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return channelRPCRequest{}, err
	}
	if req.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return channelRPCRequest{}, err
	}
	if offset != len(body) {
		return channelRPCRequest{}, fmt.Errorf("metastore: trailing channel request bytes")
	}
	return req, nil
}

func encodeChannelRPCResponse(resp channelRPCResponse) ([]byte, error) {
	return encodeChannelRPCResponseBinary(resp), nil
}

func decodeChannelRPCResponse(body []byte) (channelRPCResponse, error) {
	return decodeChannelRPCResponseBinary(body)
}

func encodeChannelRPCResponseBinary(resp channelRPCResponse) []byte {
	dst := make([]byte, 0, len(channelRPCResponseMagic)+64)
	dst = append(dst, channelRPCResponseMagic[:]...)
	dst = runtimeMetaAppendString(dst, resp.Status)
	dst = runtimeMetaAppendUvarint(dst, resp.LeaderID)
	dst = appendChannelPtr(dst, resp.Channel)
	dst = appendChannels(dst, resp.Channels)
	dst = runtimeMetaAppendChannelCursor(dst, resp.Cursor)
	dst = runtimeMetaAppendBool(dst, resp.Done)
	return dst
}

func decodeChannelRPCResponseBinary(body []byte) (channelRPCResponse, error) {
	if runtimeMetaHasMagic(body, channelRPCResponseMagicV1[:]) {
		return decodeChannelRPCResponseV1(body)
	}
	if runtimeMetaHasMagic(body, channelRPCResponseMagicV2[:]) {
		return decodeChannelRPCResponseV2(body)
	}
	if !runtimeMetaHasMagic(body, channelRPCResponseMagic[:]) {
		return channelRPCResponse{}, fmt.Errorf("metastore: invalid channel response codec")
	}
	offset := len(channelRPCResponseMagic)
	var resp channelRPCResponse
	var err error
	if resp.Status, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if resp.LeaderID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if resp.Channel, offset, err = readChannelPtr(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if resp.Channels, offset, err = readChannels(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if resp.Cursor, offset, err = runtimeMetaReadChannelCursor(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if resp.Done, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if offset != len(body) {
		return channelRPCResponse{}, fmt.Errorf("metastore: trailing channel response bytes")
	}
	return resp, nil
}

func decodeChannelRPCResponseV1(body []byte) (channelRPCResponse, error) {
	offset := len(channelRPCResponseMagicV1)
	var resp channelRPCResponse
	var err error
	if resp.Status, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if resp.LeaderID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if resp.Channel, offset, err = readChannelPtrV2(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if offset != len(body) {
		return channelRPCResponse{}, fmt.Errorf("metastore: trailing channel response bytes")
	}
	return resp, nil
}

func decodeChannelRPCResponseV2(body []byte) (channelRPCResponse, error) {
	offset := len(channelRPCResponseMagicV2)
	var resp channelRPCResponse
	var err error
	if resp.Status, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if resp.LeaderID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if resp.Channel, offset, err = readChannelPtrV2(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if resp.Channels, offset, err = readChannelsV2(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if resp.Cursor, offset, err = runtimeMetaReadChannelCursor(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if resp.Done, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return channelRPCResponse{}, err
	}
	if offset != len(body) {
		return channelRPCResponse{}, fmt.Errorf("metastore: trailing channel response bytes")
	}
	return resp, nil
}

type channelMergeItem struct {
	HashSlot uint16
	Channel  metadb.Channel
	Cursor   metadb.ChannelCursor
	Done     bool
}

type channelMergeHeap []channelMergeItem

func (h channelMergeHeap) Len() int { return len(h) }

func (h channelMergeHeap) Less(i, j int) bool {
	return channelLess(h[i].Channel, h[j].Channel)
}

func (h channelMergeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *channelMergeHeap) Push(x any) {
	*h = append(*h, x.(channelMergeItem))
}

func (h *channelMergeHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func channelLess(left, right metadb.Channel) bool {
	if len(left.ChannelID) != len(right.ChannelID) {
		return len(left.ChannelID) < len(right.ChannelID)
	}
	if left.ChannelID != right.ChannelID {
		return left.ChannelID < right.ChannelID
	}
	return left.ChannelType < right.ChannelType
}

func channelRPCOpOrDefault(op string) string {
	if op == "" {
		return channelRPCGetForPermission
	}
	return op
}

const (
	channelRPCGetForPermissionID byte = iota + 1
	channelRPCScanChannelsPageID
)

func channelRPCOpID(op string) (byte, error) {
	switch op {
	case channelRPCGetForPermission:
		return channelRPCGetForPermissionID, nil
	case channelRPCScanChannelsPage:
		return channelRPCScanChannelsPageID, nil
	default:
		return 0, fmt.Errorf("metastore: unknown channel rpc op %q", op)
	}
}

func channelRPCOpFromID(op byte) (string, error) {
	switch op {
	case channelRPCGetForPermissionID:
		return channelRPCGetForPermission, nil
	case channelRPCScanChannelsPageID:
		return channelRPCScanChannelsPage, nil
	default:
		return "", fmt.Errorf("metastore: unknown channel rpc op id %d", op)
	}
}

func appendChannelPtr(dst []byte, ch *metadb.Channel) []byte {
	if ch == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendChannel(dst, *ch)
}

func appendChannels(dst []byte, channels []metadb.Channel) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(channels)))
	for _, ch := range channels {
		dst = appendChannel(dst, ch)
	}
	return dst
}

func appendChannel(dst []byte, ch metadb.Channel) []byte {
	dst = appendChannelV2(dst, ch)
	dst = runtimeMetaAppendVarint(dst, ch.AllowStranger)
	return dst
}

func appendChannelV2(dst []byte, ch metadb.Channel) []byte {
	dst = runtimeMetaAppendString(dst, ch.ChannelID)
	dst = runtimeMetaAppendVarint(dst, ch.ChannelType)
	dst = runtimeMetaAppendVarint(dst, ch.Ban)
	dst = runtimeMetaAppendVarint(dst, ch.Disband)
	dst = runtimeMetaAppendVarint(dst, ch.SendBan)
	dst = runtimeMetaAppendUvarint(dst, ch.SubscriberMutationVersion)
	return dst
}

func readChannelPtr(body []byte, offset int) (*metadb.Channel, int, error) {
	marker, next, err := runtimeMetaReadMarker(body, offset, "channel")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	ch, next, err := readChannel(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &ch, next, nil
}

func readChannels(body []byte, offset int) ([]metadb.Channel, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	channelsLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "channel list")
	if err != nil {
		return nil, offset, err
	}
	channels := make([]metadb.Channel, channelsLen)
	for i := range channels {
		if channels[i], offset, err = readChannel(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return channels, offset, nil
}

func readChannel(body []byte, offset int) (metadb.Channel, int, error) {
	ch, offset, err := readChannelV2(body, offset)
	if err != nil {
		return metadb.Channel{}, offset, err
	}
	if ch.AllowStranger, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.Channel{}, offset, err
	}
	return ch, offset, nil
}

func readChannelPtrV2(body []byte, offset int) (*metadb.Channel, int, error) {
	marker, next, err := runtimeMetaReadMarker(body, offset, "channel")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	ch, next, err := readChannelV2(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &ch, next, nil
}

func readChannelsV2(body []byte, offset int) ([]metadb.Channel, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	channelsLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "channel list")
	if err != nil {
		return nil, offset, err
	}
	channels := make([]metadb.Channel, channelsLen)
	for i := range channels {
		if channels[i], offset, err = readChannelV2(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return channels, offset, nil
}

func readChannelV2(body []byte, offset int) (metadb.Channel, int, error) {
	var ch metadb.Channel
	var err error
	if ch.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.Channel{}, offset, err
	}
	if ch.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.Channel{}, offset, err
	}
	if ch.Ban, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.Channel{}, offset, err
	}
	if ch.Disband, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.Channel{}, offset, err
	}
	if ch.SendBan, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.Channel{}, offset, err
	}
	if ch.SubscriberMutationVersion, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.Channel{}, offset, err
	}
	return ch, offset, nil
}

func runtimeMetaAppendChannelCursor(dst []byte, cursor metadb.ChannelCursor) []byte {
	dst = runtimeMetaAppendString(dst, cursor.ChannelID)
	dst = runtimeMetaAppendVarint(dst, cursor.ChannelType)
	return dst
}

func runtimeMetaReadChannelCursor(body []byte, offset int) (metadb.ChannelCursor, int, error) {
	var cursor metadb.ChannelCursor
	var err error
	if cursor.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.ChannelCursor{}, offset, err
	}
	if cursor.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelCursor{}, offset, err
	}
	return cursor, offset, nil
}
