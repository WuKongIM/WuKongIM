package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const identityRPCServiceID uint8 = 4

const (
	identityRPCGetUser   = "get_user"
	identityRPCGetDevice = "get_device"
)

type identityRPCRequest struct {
	Op         string `json:"op"`
	SlotID     uint64 `json:"slot_id"`
	UID        string `json:"uid,omitempty"`
	DeviceFlag int64  `json:"device_flag,omitempty"`
}

type identityRPCResponse struct {
	Status   string         `json:"status"`
	LeaderID uint64         `json:"leader_id,omitempty"`
	User     *metadb.User   `json:"user,omitempty"`
	Device   *metadb.Device `json:"device,omitempty"`
}

func (r identityRPCResponse) rpcStatus() string {
	return r.Status
}

func (r identityRPCResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (s *Store) getUserAuthoritative(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, uid string) (metadb.User, error) {
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).GetUser(ctx, uid)
	}

	resp, err := s.callIdentityRPC(ctx, slotID, identityRPCRequest{
		Op:     identityRPCGetUser,
		SlotID: uint64(slotID),
		UID:    uid,
	})
	if err != nil {
		return metadb.User{}, err
	}
	if resp.User == nil {
		return metadb.User{}, metadb.ErrNotFound
	}
	return *resp.User, nil
}

func (s *Store) getDeviceAuthoritative(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, uid string, deviceFlag int64) (metadb.Device, error) {
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).GetDevice(ctx, uid, deviceFlag)
	}

	resp, err := s.callIdentityRPC(ctx, slotID, identityRPCRequest{
		Op:         identityRPCGetDevice,
		SlotID:     uint64(slotID),
		UID:        uid,
		DeviceFlag: deviceFlag,
	})
	if err != nil {
		return metadb.Device{}, err
	}
	if resp.Device == nil {
		return metadb.Device{}, metadb.ErrNotFound
	}
	return *resp.Device, nil
}

func (s *Store) callIdentityRPC(ctx context.Context, slotID multiraft.SlotID, req identityRPCRequest) (identityRPCResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return identityRPCResponse{}, err
	}
	return callAuthoritativeRPC(ctx, s, slotID, identityRPCServiceID, payload, decodeIdentityRPCResponse)
}

func (s *Store) handleIdentityRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req identityRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	slotID := multiraft.SlotID(req.SlotID)
	if statusBody, handled, err := s.handleAuthoritativeRPC(slotID, func(status string, leaderID uint64) ([]byte, error) {
		return encodeIdentityRPCResponse(identityRPCResponse{
			Status:   status,
			LeaderID: leaderID,
		})
	}); handled || err != nil {
		return statusBody, err
	}

	hashSlot := hashSlotForKey(s.cluster, req.UID)
	switch req.Op {
	case identityRPCGetUser:
		user, err := s.db.ForHashSlot(hashSlot).GetUser(ctx, req.UID)
		if errors.Is(err, metadb.ErrNotFound) {
			return encodeIdentityRPCResponse(identityRPCResponse{Status: rpcStatusNotFound})
		}
		if err != nil {
			return nil, err
		}
		return encodeIdentityRPCResponse(identityRPCResponse{
			Status: rpcStatusOK,
			User:   &user,
		})
	case identityRPCGetDevice:
		device, err := s.db.ForHashSlot(hashSlot).GetDevice(ctx, req.UID, req.DeviceFlag)
		if errors.Is(err, metadb.ErrNotFound) {
			return encodeIdentityRPCResponse(identityRPCResponse{Status: rpcStatusNotFound})
		}
		if err != nil {
			return nil, err
		}
		return encodeIdentityRPCResponse(identityRPCResponse{
			Status: rpcStatusOK,
			Device: &device,
		})
	default:
		return nil, fmt.Errorf("metastore: unknown identity rpc op %q", req.Op)
	}
}

func encodeIdentityRPCResponse(resp identityRPCResponse) ([]byte, error) {
	return json.Marshal(resp)
}

func decodeIdentityRPCResponse(body []byte) (identityRPCResponse, error) {
	var resp identityRPCResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return identityRPCResponse{}, err
	}
	return resp, nil
}
