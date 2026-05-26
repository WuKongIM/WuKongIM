package proxy

import (
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	identityRPCRequestMagic    = [...]byte{'W', 'K', 'I', 'Q', 1}
	identityRPCResponseMagic   = [...]byte{'W', 'K', 'I', 'S', 1}
	subscriberRPCRequestMagic  = [...]byte{'W', 'K', 'S', 'Q', 1}
	subscriberRPCResponseMagic = [...]byte{'W', 'K', 'S', 'S', 1}
)

const (
	identityRPCGetUserID byte = iota + 1
	identityRPCGetDeviceID
	identityRPCScanUsersPageID
)

// encodeIdentityRPCRequestBinary encodes identity lookups without JSON reflection.
func encodeIdentityRPCRequestBinary(req identityRPCRequest) ([]byte, error) {
	opID, err := identityOpID(req.Op)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(identityRPCRequestMagic)+len(req.UID)+32)
	dst = append(dst, identityRPCRequestMagic[:]...)
	dst = append(dst, opID)
	dst = runtimeMetaAppendUvarint(dst, req.SlotID)
	dst = runtimeMetaAppendString(dst, req.UID)
	dst = runtimeMetaAppendVarint(dst, req.DeviceFlag)
	dst = runtimeMetaAppendString(dst, req.After.UID)
	dst = runtimeMetaAppendVarint(dst, int64(req.Limit))
	return dst, nil
}

func decodeIdentityRPCRequest(body []byte) (identityRPCRequest, error) {
	if !isIdentityRPCRequestBinary(body) {
		return identityRPCRequest{}, fmt.Errorf("metastore: invalid identity request codec")
	}
	offset := len(identityRPCRequestMagic)
	if offset >= len(body) {
		return identityRPCRequest{}, fmt.Errorf("metastore: short identity op")
	}
	op, err := identityOpFromID(body[offset])
	if err != nil {
		return identityRPCRequest{}, err
	}
	offset++
	var req identityRPCRequest
	req.Op = op
	if req.SlotID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return identityRPCRequest{}, err
	}
	if req.UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return identityRPCRequest{}, err
	}
	if req.DeviceFlag, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return identityRPCRequest{}, err
	}
	if req.After.UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return identityRPCRequest{}, err
	}
	if req.Limit, offset, err = runtimeMetaReadInt(body, offset, "identity user scan limit"); err != nil {
		return identityRPCRequest{}, err
	}
	if offset != len(body) {
		return identityRPCRequest{}, fmt.Errorf("metastore: trailing identity request bytes")
	}
	return req, nil
}

func encodeIdentityRPCResponseBinary(resp identityRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, len(identityRPCResponseMagic)+64)
	dst = append(dst, identityRPCResponseMagic[:]...)
	dst = runtimeMetaAppendString(dst, resp.Status)
	dst = runtimeMetaAppendUvarint(dst, resp.LeaderID)
	dst = appendIdentityUserPtr(dst, resp.User)
	dst = appendIdentityDevicePtr(dst, resp.Device)
	dst = appendIdentityUsers(dst, resp.Users)
	dst = runtimeMetaAppendString(dst, resp.Cursor.UID)
	dst = runtimeMetaAppendBool(dst, resp.Done)
	return dst, nil
}

func decodeIdentityRPCResponseBinary(body []byte) (identityRPCResponse, error) {
	if !isIdentityRPCResponseBinary(body) {
		return identityRPCResponse{}, fmt.Errorf("metastore: invalid identity response codec")
	}
	offset := len(identityRPCResponseMagic)
	var resp identityRPCResponse
	var err error
	if resp.Status, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return identityRPCResponse{}, err
	}
	if resp.LeaderID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return identityRPCResponse{}, err
	}
	if resp.User, offset, err = readIdentityUserPtr(body, offset); err != nil {
		return identityRPCResponse{}, err
	}
	if resp.Device, offset, err = readIdentityDevicePtr(body, offset); err != nil {
		return identityRPCResponse{}, err
	}
	if resp.Users, offset, err = readIdentityUsers(body, offset); err != nil {
		return identityRPCResponse{}, err
	}
	if resp.Cursor.UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return identityRPCResponse{}, err
	}
	if resp.Done, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return identityRPCResponse{}, err
	}
	if offset != len(body) {
		return identityRPCResponse{}, fmt.Errorf("metastore: trailing identity response bytes")
	}
	return resp, nil
}

func isIdentityRPCRequestBinary(body []byte) bool {
	return runtimeMetaHasMagic(body, identityRPCRequestMagic[:])
}

func isIdentityRPCResponseBinary(body []byte) bool {
	return runtimeMetaHasMagic(body, identityRPCResponseMagic[:])
}

func identityOpID(op string) (byte, error) {
	switch op {
	case identityRPCGetUser:
		return identityRPCGetUserID, nil
	case identityRPCGetDevice:
		return identityRPCGetDeviceID, nil
	case identityRPCScanUsersPage:
		return identityRPCScanUsersPageID, nil
	default:
		return 0, fmt.Errorf("metastore: unknown identity rpc op %q", op)
	}
}

func identityOpFromID(op byte) (string, error) {
	switch op {
	case identityRPCGetUserID:
		return identityRPCGetUser, nil
	case identityRPCGetDeviceID:
		return identityRPCGetDevice, nil
	case identityRPCScanUsersPageID:
		return identityRPCScanUsersPage, nil
	default:
		return "", fmt.Errorf("metastore: unknown identity rpc op id %d", op)
	}
}

func appendIdentityUserPtr(dst []byte, user *metadb.User) []byte {
	if user == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = runtimeMetaAppendString(dst, user.UID)
	dst = runtimeMetaAppendString(dst, user.Token)
	dst = runtimeMetaAppendVarint(dst, user.DeviceFlag)
	dst = runtimeMetaAppendVarint(dst, user.DeviceLevel)
	return dst
}

func readIdentityUserPtr(body []byte, offset int) (*metadb.User, int, error) {
	marker, next, err := runtimeMetaReadMarker(body, offset, "identity user")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	var user metadb.User
	if user.UID, next, err = runtimeMetaReadString(body, next); err != nil {
		return nil, offset, err
	}
	if user.Token, next, err = runtimeMetaReadString(body, next); err != nil {
		return nil, offset, err
	}
	if user.DeviceFlag, next, err = runtimeMetaReadVarint(body, next); err != nil {
		return nil, offset, err
	}
	if user.DeviceLevel, next, err = runtimeMetaReadVarint(body, next); err != nil {
		return nil, offset, err
	}
	return &user, next, nil
}

func appendIdentityDevicePtr(dst []byte, device *metadb.Device) []byte {
	if device == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = runtimeMetaAppendString(dst, device.UID)
	dst = runtimeMetaAppendVarint(dst, device.DeviceFlag)
	dst = runtimeMetaAppendString(dst, device.Token)
	dst = runtimeMetaAppendVarint(dst, device.DeviceLevel)
	return dst
}

func readIdentityDevicePtr(body []byte, offset int) (*metadb.Device, int, error) {
	marker, next, err := runtimeMetaReadMarker(body, offset, "identity device")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	var device metadb.Device
	if device.UID, next, err = runtimeMetaReadString(body, next); err != nil {
		return nil, offset, err
	}
	if device.DeviceFlag, next, err = runtimeMetaReadVarint(body, next); err != nil {
		return nil, offset, err
	}
	if device.Token, next, err = runtimeMetaReadString(body, next); err != nil {
		return nil, offset, err
	}
	if device.DeviceLevel, next, err = runtimeMetaReadVarint(body, next); err != nil {
		return nil, offset, err
	}
	return &device, next, nil
}

func appendIdentityUsers(dst []byte, users []metadb.User) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(users)))
	for _, user := range users {
		dst = runtimeMetaAppendString(dst, user.UID)
		dst = runtimeMetaAppendString(dst, user.Token)
		dst = runtimeMetaAppendVarint(dst, user.DeviceFlag)
		dst = runtimeMetaAppendVarint(dst, user.DeviceLevel)
	}
	return dst
}

func readIdentityUsers(body []byte, offset int) ([]metadb.User, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	usersLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "identity users")
	if err != nil {
		return nil, offset, err
	}
	users := make([]metadb.User, usersLen)
	for i := range users {
		if users[i].UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
		if users[i].Token, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
		if users[i].DeviceFlag, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
		if users[i].DeviceLevel, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return users, offset, nil
}

// encodeSubscriberRPCRequestBinary encodes subscriber page requests without JSON reflection.
func encodeSubscriberRPCRequestBinary(req subscriberRPCRequest) ([]byte, error) {
	dst := make([]byte, 0, len(subscriberRPCRequestMagic)+len(req.ChannelID)+len(req.AfterUID)+64)
	dst = append(dst, subscriberRPCRequestMagic[:]...)
	dst = runtimeMetaAppendUvarint(dst, req.SlotID)
	dst = runtimeMetaAppendUvarint(dst, uint64(req.HashSlot))
	dst = runtimeMetaAppendString(dst, req.ChannelID)
	dst = runtimeMetaAppendVarint(dst, req.ChannelType)
	dst = runtimeMetaAppendBool(dst, req.Snapshot)
	dst = runtimeMetaAppendString(dst, req.AfterUID)
	dst = runtimeMetaAppendVarint(dst, int64(req.Limit))
	dst = runtimeMetaAppendString(dst, req.ContainsUID)
	dst = runtimeMetaAppendBool(dst, req.HasAny)
	return dst, nil
}

func decodeSubscriberRPCRequest(body []byte) (subscriberRPCRequest, error) {
	if !isSubscriberRPCRequestBinary(body) {
		return subscriberRPCRequest{}, fmt.Errorf("metastore: invalid subscriber request codec")
	}
	offset := len(subscriberRPCRequestMagic)
	var req subscriberRPCRequest
	var err error
	if req.SlotID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return subscriberRPCRequest{}, err
	}
	hashSlot, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return subscriberRPCRequest{}, err
	}
	if hashSlot > uint64(^uint16(0)) {
		return subscriberRPCRequest{}, fmt.Errorf("metastore: subscriber hash slot overflows uint16")
	}
	req.HashSlot = uint16(hashSlot)
	offset = next
	if req.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return subscriberRPCRequest{}, err
	}
	if req.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return subscriberRPCRequest{}, err
	}
	if req.Snapshot, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return subscriberRPCRequest{}, err
	}
	if req.AfterUID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return subscriberRPCRequest{}, err
	}
	if req.Limit, offset, err = runtimeMetaReadInt(body, offset, "subscriber limit"); err != nil {
		return subscriberRPCRequest{}, err
	}
	if req.ContainsUID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return subscriberRPCRequest{}, err
	}
	if req.HasAny, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return subscriberRPCRequest{}, err
	}
	if offset != len(body) {
		return subscriberRPCRequest{}, fmt.Errorf("metastore: trailing subscriber request bytes")
	}
	return req, nil
}

func encodeSubscriberRPCResponseBinary(resp subscriberRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, len(subscriberRPCResponseMagic)+len(resp.NextCursor)+len(resp.UIDs)*16+64)
	dst = append(dst, subscriberRPCResponseMagic[:]...)
	dst = runtimeMetaAppendString(dst, resp.Status)
	dst = runtimeMetaAppendUvarint(dst, resp.LeaderID)
	dst = appendProxyStrings(dst, resp.UIDs)
	dst = runtimeMetaAppendString(dst, resp.NextCursor)
	dst = runtimeMetaAppendBool(dst, resp.Done)
	dst = runtimeMetaAppendBool(dst, resp.Contains)
	dst = runtimeMetaAppendBool(dst, resp.HasAny)
	return dst, nil
}

func decodeSubscriberRPCResponseBinary(body []byte) (subscriberRPCResponse, error) {
	if !isSubscriberRPCResponseBinary(body) {
		return subscriberRPCResponse{}, fmt.Errorf("metastore: invalid subscriber response codec")
	}
	offset := len(subscriberRPCResponseMagic)
	var resp subscriberRPCResponse
	var err error
	if resp.Status, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return subscriberRPCResponse{}, err
	}
	if resp.LeaderID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return subscriberRPCResponse{}, err
	}
	if resp.UIDs, offset, err = readProxyStrings(body, offset, "subscriber uids"); err != nil {
		return subscriberRPCResponse{}, err
	}
	if resp.NextCursor, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return subscriberRPCResponse{}, err
	}
	if resp.Done, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return subscriberRPCResponse{}, err
	}
	if resp.Contains, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return subscriberRPCResponse{}, err
	}
	if resp.HasAny, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return subscriberRPCResponse{}, err
	}
	if offset != len(body) {
		return subscriberRPCResponse{}, fmt.Errorf("metastore: trailing subscriber response bytes")
	}
	return resp, nil
}

func isSubscriberRPCRequestBinary(body []byte) bool {
	return runtimeMetaHasMagic(body, subscriberRPCRequestMagic[:])
}

func isSubscriberRPCResponseBinary(body []byte) bool {
	return runtimeMetaHasMagic(body, subscriberRPCResponseMagic[:])
}

func appendProxyStrings(dst []byte, values []string) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = runtimeMetaAppendString(dst, value)
	}
	return dst
}

func readProxyStrings(body []byte, offset int, label string) ([]string, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	valuesLen, err := runtimeMetaCollectionLen(count, len(body)-offset, label)
	if err != nil {
		return nil, offset, err
	}
	values := make([]string, valuesLen)
	for i := range values {
		if values[i], offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return values, offset, nil
}
