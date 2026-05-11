package proxy

import (
	"context"
	"errors"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const channelRPCServiceID uint8 = 12

var (
	channelRPCRequestMagic  = [...]byte{'W', 'K', 'C', 'Q', 1}
	channelRPCResponseMagic = [...]byte{'W', 'K', 'C', 'S', 1}
)

type channelRPCRequest struct {
	SlotID      uint64 `json:"slot_id"`
	HashSlot    uint16 `json:"hash_slot,omitempty"`
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
}

type channelRPCResponse struct {
	Status   string          `json:"status"`
	LeaderID uint64          `json:"leader_id,omitempty"`
	Channel  *metadb.Channel `json:"channel,omitempty"`
}

func (r channelRPCResponse) rpcStatus() string {
	return r.Status
}

func (r channelRPCResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (s *Store) getChannelForPermissionAuthoritative(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, channelID string, channelType int64) (metadb.Channel, error) {
	resp, err := s.callChannelRPC(ctx, slotID, channelRPCRequest{
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
}

func encodeChannelRPCRequestBinary(req channelRPCRequest) ([]byte, error) {
	dst := make([]byte, 0, len(channelRPCRequestMagic)+len(req.ChannelID)+32)
	dst = append(dst, channelRPCRequestMagic[:]...)
	dst = runtimeMetaAppendUvarint(dst, req.SlotID)
	dst = runtimeMetaAppendUvarint(dst, uint64(req.HashSlot))
	dst = runtimeMetaAppendString(dst, req.ChannelID)
	dst = runtimeMetaAppendVarint(dst, req.ChannelType)
	return dst, nil
}

func decodeChannelRPCRequest(body []byte) (channelRPCRequest, error) {
	if !runtimeMetaHasMagic(body, channelRPCRequestMagic[:]) {
		return channelRPCRequest{}, fmt.Errorf("metastore: invalid channel request codec")
	}
	offset := len(channelRPCRequestMagic)
	var req channelRPCRequest
	var err error
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
	return dst
}

func decodeChannelRPCResponseBinary(body []byte) (channelRPCResponse, error) {
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
	if offset != len(body) {
		return channelRPCResponse{}, fmt.Errorf("metastore: trailing channel response bytes")
	}
	return resp, nil
}

func appendChannelPtr(dst []byte, ch *metadb.Channel) []byte {
	if ch == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
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
	var ch metadb.Channel
	if ch.ChannelID, next, err = runtimeMetaReadString(body, next); err != nil {
		return nil, offset, err
	}
	if ch.ChannelType, next, err = runtimeMetaReadVarint(body, next); err != nil {
		return nil, offset, err
	}
	if ch.Ban, next, err = runtimeMetaReadVarint(body, next); err != nil {
		return nil, offset, err
	}
	if ch.Disband, next, err = runtimeMetaReadVarint(body, next); err != nil {
		return nil, offset, err
	}
	if ch.SendBan, next, err = runtimeMetaReadVarint(body, next); err != nil {
		return nil, offset, err
	}
	if ch.SubscriberMutationVersion, next, err = runtimeMetaReadUvarint(body, next); err != nil {
		return nil, offset, err
	}
	return &ch, next, nil
}
