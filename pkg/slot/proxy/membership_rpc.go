package proxy

import (
	"context"
	"fmt"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	membershipRPCServiceID uint8 = clusternet.RPCSlotUserMembership
	membershipRPCMaxRows         = 4096
)

const (
	membershipRPCListOrdinary = "list_ordinary"
	membershipRPCGetOrdinary  = "get_ordinary"
	membershipRPCListCMD      = "list_cmd"
)

var (
	membershipRPCRequestMagic  = [...]byte{'W', 'K', 'M', 'Q', 1}
	membershipRPCResponseMagic = [...]byte{'W', 'K', 'M', 'S', 1}
)

const (
	membershipRPCListOrdinaryID byte = iota + 1
	membershipRPCGetOrdinaryID
	membershipRPCListCMDID
)

type membershipRPCRequest struct {
	Op             string
	SlotID         uint64
	UID            string
	ChannelID      string
	ChannelType    int64
	OrdinaryCursor metadb.UserChannelMembershipCursor
	CMDCursor      metadb.UserCMDChannelMembershipCursor
	Limit          int
}

type membershipRPCResponse struct {
	Status         string
	LeaderID       uint64
	Membership     *metadb.UserChannelMembership
	Memberships    []metadb.UserChannelMembership
	OrdinaryCursor metadb.UserChannelMembershipCursor
	CMDMemberships []metadb.UserCMDChannelMembership
	CMDCursor      metadb.UserCMDChannelMembershipCursor
	Done           bool
}

func (r membershipRPCResponse) rpcStatus() string { return r.Status }

func (r membershipRPCResponse) rpcLeaderID() uint64 { return r.LeaderID }

// ListUserChannelMembershipPage reads one UID-owned ordinary directory page
// from the current Slot leader.
func (s *Store) ListUserChannelMembershipPage(ctx context.Context, uid string, after metadb.UserChannelMembershipCursor, limit int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	if s.shouldServeSlotLocally(slotID) {
		return s.db.MetaDB().HashSlot(metadb.HashSlot(hashSlot)).ListUserChannelMembershipPage(ctx, uid, after, limit)
	}
	resp, err := s.callMembershipRPC(ctx, slotID, membershipRPCRequest{
		Op: membershipRPCListOrdinary, SlotID: uint64(slotID), UID: uid,
		OrdinaryCursor: after, Limit: limit,
	})
	if err != nil {
		return nil, metadb.UserChannelMembershipCursor{}, false, err
	}
	return append([]metadb.UserChannelMembership(nil), resp.Memberships...), resp.OrdinaryCursor, resp.Done, nil
}

// GetUserChannelMembership reads one UID-owned ordinary membership from the
// current Slot leader.
func (s *Store) GetUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserChannelMembership, bool, error) {
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	if s.shouldServeSlotLocally(slotID) {
		return s.db.MetaDB().HashSlot(metadb.HashSlot(hashSlot)).GetUserChannelMembership(ctx, uid, channelID, channelType)
	}
	resp, err := s.callMembershipRPC(ctx, slotID, membershipRPCRequest{
		Op: membershipRPCGetOrdinary, SlotID: uint64(slotID), UID: uid,
		ChannelID: channelID, ChannelType: channelType,
	})
	if err != nil {
		return metadb.UserChannelMembership{}, false, err
	}
	if resp.Status == rpcStatusNotFound || resp.Membership == nil {
		return metadb.UserChannelMembership{}, false, nil
	}
	return *resp.Membership, true, nil
}

// ListUserCMDChannelMembershipPage reads one UID-owned CMD directory page
// from the current Slot leader.
func (s *Store) ListUserCMDChannelMembershipPage(ctx context.Context, uid string, after metadb.UserCMDChannelMembershipCursor, limit int) ([]metadb.UserCMDChannelMembership, metadb.UserCMDChannelMembershipCursor, bool, error) {
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	if s.shouldServeSlotLocally(slotID) {
		return s.db.MetaDB().HashSlot(metadb.HashSlot(hashSlot)).ListUserCMDChannelMembershipPage(ctx, uid, after, limit)
	}
	resp, err := s.callMembershipRPC(ctx, slotID, membershipRPCRequest{
		Op: membershipRPCListCMD, SlotID: uint64(slotID), UID: uid,
		CMDCursor: after, Limit: limit,
	})
	if err != nil {
		return nil, metadb.UserCMDChannelMembershipCursor{}, false, err
	}
	return append([]metadb.UserCMDChannelMembership(nil), resp.CMDMemberships...), resp.CMDCursor, resp.Done, nil
}

func (s *Store) callMembershipRPC(ctx context.Context, slotID multiraft.SlotID, req membershipRPCRequest) (membershipRPCResponse, error) {
	payload, err := encodeMembershipRPCRequest(req)
	if err != nil {
		return membershipRPCResponse{}, err
	}
	return callAuthoritativeRPC(ctx, s, slotID, membershipRPCServiceID, payload, decodeMembershipRPCResponse)
}

func (s *Store) handleMembershipRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeMembershipRPCRequest(body)
	if err != nil {
		return nil, err
	}
	slotID := multiraft.SlotID(req.SlotID)
	uidSlotID := s.cluster.SlotForKey(req.UID)
	if slotID != uidSlotID {
		return nil, fmt.Errorf("metastore: uid slot mismatch: requested=%d authoritative=%d", slotID, uidSlotID)
	}
	if statusBody, handled, err := s.handleAuthoritativeRPC(slotID, func(status string, leaderID uint64) ([]byte, error) {
		return encodeMembershipRPCResponse(membershipRPCResponse{Status: status, LeaderID: leaderID})
	}); handled || err != nil {
		return statusBody, err
	}
	hashSlot := hashSlotForKey(s.cluster, req.UID)
	switch req.Op {
	case membershipRPCListOrdinary:
		rows, cursor, done, err := s.db.MetaDB().HashSlot(metadb.HashSlot(hashSlot)).ListUserChannelMembershipPage(ctx, req.UID, req.OrdinaryCursor, req.Limit)
		if err != nil {
			return nil, err
		}
		return encodeMembershipRPCResponse(membershipRPCResponse{
			Status: rpcStatusOK, Memberships: rows, OrdinaryCursor: cursor, Done: done,
		})
	case membershipRPCGetOrdinary:
		row, ok, err := s.db.MetaDB().HashSlot(metadb.HashSlot(hashSlot)).GetUserChannelMembership(ctx, req.UID, req.ChannelID, req.ChannelType)
		if err != nil {
			return nil, err
		}
		if !ok {
			return encodeMembershipRPCResponse(membershipRPCResponse{Status: rpcStatusNotFound})
		}
		return encodeMembershipRPCResponse(membershipRPCResponse{Status: rpcStatusOK, Membership: &row})
	case membershipRPCListCMD:
		rows, cursor, done, err := s.db.MetaDB().HashSlot(metadb.HashSlot(hashSlot)).ListUserCMDChannelMembershipPage(ctx, req.UID, req.CMDCursor, req.Limit)
		if err != nil {
			return nil, err
		}
		return encodeMembershipRPCResponse(membershipRPCResponse{
			Status: rpcStatusOK, CMDMemberships: rows, CMDCursor: cursor, Done: done,
		})
	default:
		return nil, fmt.Errorf("metastore: unknown membership rpc op %q", req.Op)
	}
}

func encodeMembershipRPCRequest(req membershipRPCRequest) ([]byte, error) {
	op, err := membershipRPCOpID(req.Op)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(membershipRPCRequestMagic)+len(req.UID)+len(req.ChannelID)+64)
	dst = append(dst, membershipRPCRequestMagic[:]...)
	dst = append(dst, op)
	dst = runtimeMetaAppendUvarint(dst, req.SlotID)
	dst = runtimeMetaAppendString(dst, req.UID)
	dst = runtimeMetaAppendString(dst, req.ChannelID)
	dst = runtimeMetaAppendVarint(dst, req.ChannelType)
	dst = appendOrdinaryMembershipCursor(dst, req.OrdinaryCursor)
	dst = appendCMDMembershipCursor(dst, req.CMDCursor)
	dst = runtimeMetaAppendVarint(dst, int64(req.Limit))
	return dst, nil
}

func decodeMembershipRPCRequest(body []byte) (membershipRPCRequest, error) {
	if !runtimeMetaHasMagic(body, membershipRPCRequestMagic[:]) {
		return membershipRPCRequest{}, fmt.Errorf("metastore: invalid membership request codec")
	}
	offset := len(membershipRPCRequestMagic)
	if offset >= len(body) {
		return membershipRPCRequest{}, fmt.Errorf("metastore: short membership op")
	}
	op, err := membershipRPCOpFromID(body[offset])
	if err != nil {
		return membershipRPCRequest{}, err
	}
	offset++
	req := membershipRPCRequest{Op: op}
	if req.SlotID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return membershipRPCRequest{}, err
	}
	if req.UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return membershipRPCRequest{}, err
	}
	if req.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return membershipRPCRequest{}, err
	}
	if req.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return membershipRPCRequest{}, err
	}
	if req.OrdinaryCursor, offset, err = readOrdinaryMembershipCursor(body, offset); err != nil {
		return membershipRPCRequest{}, err
	}
	if req.CMDCursor, offset, err = readCMDMembershipCursor(body, offset); err != nil {
		return membershipRPCRequest{}, err
	}
	if req.Limit, offset, err = runtimeMetaReadInt(body, offset, "membership limit"); err != nil {
		return membershipRPCRequest{}, err
	}
	if offset != len(body) {
		return membershipRPCRequest{}, fmt.Errorf("metastore: trailing membership request bytes")
	}
	return req, nil
}

func encodeMembershipRPCResponse(resp membershipRPCResponse) ([]byte, error) {
	if len(resp.Memberships) > membershipRPCMaxRows || len(resp.CMDMemberships) > membershipRPCMaxRows {
		return nil, fmt.Errorf("metastore: membership response exceeds %d rows", membershipRPCMaxRows)
	}
	dst := make([]byte, 0, len(membershipRPCResponseMagic)+128)
	dst = append(dst, membershipRPCResponseMagic[:]...)
	dst = runtimeMetaAppendString(dst, resp.Status)
	dst = runtimeMetaAppendUvarint(dst, resp.LeaderID)
	dst = appendOrdinaryMembershipPtr(dst, resp.Membership)
	dst = appendOrdinaryMemberships(dst, resp.Memberships)
	dst = appendOrdinaryMembershipCursor(dst, resp.OrdinaryCursor)
	dst = appendCMDMemberships(dst, resp.CMDMemberships)
	dst = appendCMDMembershipCursor(dst, resp.CMDCursor)
	dst = runtimeMetaAppendBool(dst, resp.Done)
	return dst, nil
}

func decodeMembershipRPCResponse(body []byte) (membershipRPCResponse, error) {
	if !runtimeMetaHasMagic(body, membershipRPCResponseMagic[:]) {
		return membershipRPCResponse{}, fmt.Errorf("metastore: invalid membership response codec")
	}
	offset := len(membershipRPCResponseMagic)
	var resp membershipRPCResponse
	var err error
	if resp.Status, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return membershipRPCResponse{}, err
	}
	if resp.LeaderID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return membershipRPCResponse{}, err
	}
	if resp.Membership, offset, err = readOrdinaryMembershipPtr(body, offset); err != nil {
		return membershipRPCResponse{}, err
	}
	if resp.Memberships, offset, err = readOrdinaryMemberships(body, offset); err != nil {
		return membershipRPCResponse{}, err
	}
	if resp.OrdinaryCursor, offset, err = readOrdinaryMembershipCursor(body, offset); err != nil {
		return membershipRPCResponse{}, err
	}
	if resp.CMDMemberships, offset, err = readCMDMemberships(body, offset); err != nil {
		return membershipRPCResponse{}, err
	}
	if resp.CMDCursor, offset, err = readCMDMembershipCursor(body, offset); err != nil {
		return membershipRPCResponse{}, err
	}
	if resp.Done, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return membershipRPCResponse{}, err
	}
	if offset != len(body) {
		return membershipRPCResponse{}, fmt.Errorf("metastore: trailing membership response bytes")
	}
	return resp, nil
}

func membershipRPCOpID(op string) (byte, error) {
	switch op {
	case membershipRPCListOrdinary:
		return membershipRPCListOrdinaryID, nil
	case membershipRPCGetOrdinary:
		return membershipRPCGetOrdinaryID, nil
	case membershipRPCListCMD:
		return membershipRPCListCMDID, nil
	default:
		return 0, fmt.Errorf("metastore: unknown membership rpc op %q", op)
	}
}

func membershipRPCOpFromID(op byte) (string, error) {
	switch op {
	case membershipRPCListOrdinaryID:
		return membershipRPCListOrdinary, nil
	case membershipRPCGetOrdinaryID:
		return membershipRPCGetOrdinary, nil
	case membershipRPCListCMDID:
		return membershipRPCListCMD, nil
	default:
		return "", fmt.Errorf("metastore: unknown membership rpc op id %d", op)
	}
}

func appendOrdinaryMembershipCursor(dst []byte, cursor metadb.UserChannelMembershipCursor) []byte {
	dst = runtimeMetaAppendVarint(dst, cursor.ActivatedAt)
	dst = runtimeMetaAppendString(dst, cursor.ChannelID)
	return runtimeMetaAppendVarint(dst, cursor.ChannelType)
}

func readOrdinaryMembershipCursor(body []byte, offset int) (metadb.UserChannelMembershipCursor, int, error) {
	var cursor metadb.UserChannelMembershipCursor
	var err error
	if cursor.ActivatedAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.UserChannelMembershipCursor{}, offset, err
	}
	if cursor.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.UserChannelMembershipCursor{}, offset, err
	}
	if cursor.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.UserChannelMembershipCursor{}, offset, err
	}
	return cursor, offset, nil
}

func appendCMDMembershipCursor(dst []byte, cursor metadb.UserCMDChannelMembershipCursor) []byte {
	dst = runtimeMetaAppendString(dst, cursor.CommandChannelID)
	return runtimeMetaAppendVarint(dst, cursor.ChannelType)
}

func readCMDMembershipCursor(body []byte, offset int) (metadb.UserCMDChannelMembershipCursor, int, error) {
	var cursor metadb.UserCMDChannelMembershipCursor
	var err error
	if cursor.CommandChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.UserCMDChannelMembershipCursor{}, offset, err
	}
	if cursor.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.UserCMDChannelMembershipCursor{}, offset, err
	}
	return cursor, offset, nil
}

func appendOrdinaryMembershipPtr(dst []byte, row *metadb.UserChannelMembership) []byte {
	if row == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendOrdinaryMembership(dst, *row)
}

func readOrdinaryMembershipPtr(body []byte, offset int) (*metadb.UserChannelMembership, int, error) {
	marker, next, err := runtimeMetaReadMarker(body, offset, "ordinary membership")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	row, next, err := readOrdinaryMembership(body, next)
	return &row, next, err
}

func appendOrdinaryMemberships(dst []byte, rows []metadb.UserChannelMembership) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(rows)))
	for _, row := range rows {
		dst = appendOrdinaryMembership(dst, row)
	}
	return dst
}

func readOrdinaryMemberships(body []byte, offset int) ([]metadb.UserChannelMembership, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if count > membershipRPCMaxRows {
		return nil, offset, fmt.Errorf("metastore: ordinary membership count %d exceeds limit", count)
	}
	rows := make([]metadb.UserChannelMembership, int(count))
	offset = next
	for index := range rows {
		if rows[index], offset, err = readOrdinaryMembership(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return rows, offset, nil
}

func appendOrdinaryMembership(dst []byte, row metadb.UserChannelMembership) []byte {
	dst = runtimeMetaAppendString(dst, row.UID)
	dst = runtimeMetaAppendString(dst, row.ChannelID)
	dst = runtimeMetaAppendVarint(dst, row.ChannelType)
	dst = runtimeMetaAppendUvarint(dst, row.JoinSeq)
	dst = runtimeMetaAppendUvarint(dst, row.ReadSeq)
	dst = runtimeMetaAppendUvarint(dst, row.DeletedToSeq)
	dst = runtimeMetaAppendVarint(dst, row.ActivatedAt)
	dst = runtimeMetaAppendBool(dst, row.Tombstone)
	dst = runtimeMetaAppendVarint(dst, row.TombstoneAt)
	dst = runtimeMetaAppendUvarint(dst, row.SourceVersion)
	return runtimeMetaAppendVarint(dst, row.UpdatedAt)
}

func readOrdinaryMembership(body []byte, offset int) (metadb.UserChannelMembership, int, error) {
	var row metadb.UserChannelMembership
	var err error
	if row.UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return row, offset, err
	}
	if row.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return row, offset, err
	}
	if row.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return row, offset, err
	}
	if row.JoinSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return row, offset, err
	}
	if row.ReadSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return row, offset, err
	}
	if row.DeletedToSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return row, offset, err
	}
	if row.ActivatedAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return row, offset, err
	}
	if row.Tombstone, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return row, offset, err
	}
	if row.TombstoneAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return row, offset, err
	}
	if row.SourceVersion, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return row, offset, err
	}
	if row.UpdatedAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return row, offset, err
	}
	return row, offset, nil
}

func appendCMDMemberships(dst []byte, rows []metadb.UserCMDChannelMembership) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(rows)))
	for _, row := range rows {
		dst = runtimeMetaAppendString(dst, row.UID)
		dst = runtimeMetaAppendString(dst, row.CommandChannelID)
		dst = runtimeMetaAppendVarint(dst, row.ChannelType)
		dst = runtimeMetaAppendUvarint(dst, row.StartSeq)
		dst = runtimeMetaAppendUvarint(dst, row.AckSeq)
		dst = runtimeMetaAppendBool(dst, row.Tombstone)
		dst = runtimeMetaAppendVarint(dst, row.TombstoneAt)
		dst = runtimeMetaAppendVarint(dst, row.UpdatedAt)
	}
	return dst
}

func readCMDMemberships(body []byte, offset int) ([]metadb.UserCMDChannelMembership, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if count > membershipRPCMaxRows {
		return nil, offset, fmt.Errorf("metastore: cmd membership count %d exceeds limit", count)
	}
	rows := make([]metadb.UserCMDChannelMembership, int(count))
	offset = next
	for index := range rows {
		row := &rows[index]
		if row.UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
		if row.CommandChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
		if row.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
		if row.StartSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
			return nil, offset, err
		}
		if row.AckSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
			return nil, offset, err
		}
		if row.Tombstone, offset, err = runtimeMetaReadBool(body, offset); err != nil {
			return nil, offset, err
		}
		if row.TombstoneAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
		if row.UpdatedAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return rows, offset, nil
}
