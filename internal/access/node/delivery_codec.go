package node

import (
	"fmt"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

var (
	deliveryRPCRequestMagic     = [...]byte{'W', 'K', 'V', 'D', 1}
	deliveryRPCResponseMagic    = [...]byte{'W', 'K', 'V', 'd', 1}
	deliveryFanoutRequestMagic  = [...]byte{'W', 'K', 'V', 'F', 1}
	deliveryFanoutResponseMagic = [...]byte{'W', 'K', 'V', 'f', 1}
)

const maxDeliveryRPCCollectionLen = 4096

// deliveryPushRequest is the deterministic binary DTO for owner-node delivery push calls.
type deliveryPushRequest struct {
	// Command carries one owner-node delivery push batch.
	Command runtimedelivery.PushCommand
}

// deliveryPushResponse is the deterministic binary DTO returned by delivery push calls.
type deliveryPushResponse struct {
	// Status is one of the stable delivery RPC status strings.
	Status string
	// Result reports how the owner node classified pushed routes.
	Result runtimedelivery.PushResult
}

// deliveryFanoutRequest is the deterministic binary DTO for authority-node fanout calls.
type deliveryFanoutRequest struct {
	// Task carries one partition-scoped fanout task.
	Task runtimedelivery.FanoutTask
}

// deliveryFanoutResponse is the deterministic binary DTO returned by fanout calls.
type deliveryFanoutResponse struct {
	// Status is one of the stable delivery RPC status strings.
	Status string
}

func encodeDeliveryPushRequest(req deliveryPushRequest) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, deliveryRPCRequestMagic[:]...)
	dst = appendDeliveryPushCommand(dst, req.Command)
	return dst, nil
}

func decodeDeliveryPushRequest(body []byte) (deliveryPushRequest, error) {
	if !hasMagic(body, deliveryRPCRequestMagic[:]) {
		return deliveryPushRequest{}, fmt.Errorf("internal/access/node: invalid delivery request codec")
	}
	offset := len(deliveryRPCRequestMagic)
	var req deliveryPushRequest
	var err error
	if req.Command, offset, err = readDeliveryPushCommand(body, offset); err != nil {
		return deliveryPushRequest{}, err
	}
	if offset != len(body) {
		return deliveryPushRequest{}, fmt.Errorf("internal/access/node: trailing delivery request bytes")
	}
	return req, nil
}

func encodeDeliveryPushResponse(resp deliveryPushResponse) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, deliveryRPCResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendDeliveryPushResult(dst, resp.Result)
	return dst, nil
}

func decodeDeliveryPushResponse(body []byte) (deliveryPushResponse, error) {
	if !hasMagic(body, deliveryRPCResponseMagic[:]) {
		return deliveryPushResponse{}, fmt.Errorf("internal/access/node: invalid delivery response codec")
	}
	offset := len(deliveryRPCResponseMagic)
	var resp deliveryPushResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return deliveryPushResponse{}, err
	}
	if resp.Result, offset, err = readDeliveryPushResult(body, offset); err != nil {
		return deliveryPushResponse{}, err
	}
	if offset != len(body) {
		return deliveryPushResponse{}, fmt.Errorf("internal/access/node: trailing delivery response bytes")
	}
	return resp, nil
}

func encodeDeliveryFanoutRequest(req deliveryFanoutRequest) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, deliveryFanoutRequestMagic[:]...)
	dst = appendDeliveryFanoutTask(dst, req.Task)
	return dst, nil
}

func decodeDeliveryFanoutRequest(body []byte) (deliveryFanoutRequest, error) {
	if !hasMagic(body, deliveryFanoutRequestMagic[:]) {
		return deliveryFanoutRequest{}, fmt.Errorf("internal/access/node: invalid delivery fanout request codec")
	}
	offset := len(deliveryFanoutRequestMagic)
	var req deliveryFanoutRequest
	var err error
	if req.Task, offset, err = readDeliveryFanoutTask(body, offset); err != nil {
		return deliveryFanoutRequest{}, err
	}
	if offset != len(body) {
		return deliveryFanoutRequest{}, fmt.Errorf("internal/access/node: trailing delivery fanout request bytes")
	}
	return req, nil
}

func encodeDeliveryFanoutResponse(resp deliveryFanoutResponse) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, deliveryFanoutResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	return dst, nil
}

func decodeDeliveryFanoutResponse(body []byte) (deliveryFanoutResponse, error) {
	if !hasMagic(body, deliveryFanoutResponseMagic[:]) {
		return deliveryFanoutResponse{}, fmt.Errorf("internal/access/node: invalid delivery fanout response codec")
	}
	offset := len(deliveryFanoutResponseMagic)
	var resp deliveryFanoutResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return deliveryFanoutResponse{}, err
	}
	if offset != len(body) {
		return deliveryFanoutResponse{}, fmt.Errorf("internal/access/node: trailing delivery fanout response bytes")
	}
	return resp, nil
}

func appendDeliveryPushCommand(dst []byte, cmd runtimedelivery.PushCommand) []byte {
	dst = appendUvarint(dst, cmd.OwnerNodeID)
	dst = appendDeliveryEnvelope(dst, cmd.Envelope)
	return appendDeliveryRoutes(dst, cmd.Routes)
}

func readDeliveryPushCommand(body []byte, offset int) (runtimedelivery.PushCommand, int, error) {
	var cmd runtimedelivery.PushCommand
	var err error
	if cmd.OwnerNodeID, offset, err = readUvarint(body, offset); err != nil {
		return runtimedelivery.PushCommand{}, offset, err
	}
	if cmd.Envelope, offset, err = readDeliveryEnvelope(body, offset); err != nil {
		return runtimedelivery.PushCommand{}, offset, err
	}
	if cmd.Routes, offset, err = readDeliveryRoutes(body, offset); err != nil {
		return runtimedelivery.PushCommand{}, offset, err
	}
	return cmd, offset, nil
}

func appendDeliveryFanoutTask(dst []byte, task runtimedelivery.FanoutTask) []byte {
	dst = appendDeliveryEnvelope(dst, task.Envelope)
	dst = appendDeliveryPartition(dst, task.Partition)
	dst = appendString(dst, task.Cursor)
	return appendVarint(dst, int64(task.Attempt))
}

func readDeliveryFanoutTask(body []byte, offset int) (runtimedelivery.FanoutTask, int, error) {
	var task runtimedelivery.FanoutTask
	var attempt int64
	var err error
	if task.Envelope, offset, err = readDeliveryEnvelope(body, offset); err != nil {
		return runtimedelivery.FanoutTask{}, offset, err
	}
	if task.Partition, offset, err = readDeliveryPartition(body, offset); err != nil {
		return runtimedelivery.FanoutTask{}, offset, err
	}
	if task.Cursor, offset, err = readString(body, offset); err != nil {
		return runtimedelivery.FanoutTask{}, offset, err
	}
	if attempt, offset, err = readVarint(body, offset); err != nil {
		return runtimedelivery.FanoutTask{}, offset, err
	}
	task.Attempt = int(attempt)
	return task, offset, nil
}

func appendDeliveryPartition(dst []byte, partition runtimedelivery.Partition) []byte {
	dst = appendUvarint(dst, uint64(partition.ID))
	dst = appendUvarint(dst, partition.LeaderNodeID)
	dst = appendUvarint(dst, uint64(partition.HashSlotStart))
	return appendUvarint(dst, uint64(partition.HashSlotEnd))
}

func readDeliveryPartition(body []byte, offset int) (runtimedelivery.Partition, int, error) {
	var partition runtimedelivery.Partition
	var id, start, end uint64
	var err error
	if id, offset, err = readUvarint(body, offset); err != nil {
		return runtimedelivery.Partition{}, offset, err
	}
	if partition.LeaderNodeID, offset, err = readUvarint(body, offset); err != nil {
		return runtimedelivery.Partition{}, offset, err
	}
	if start, offset, err = readUvarint(body, offset); err != nil {
		return runtimedelivery.Partition{}, offset, err
	}
	if end, offset, err = readUvarint(body, offset); err != nil {
		return runtimedelivery.Partition{}, offset, err
	}
	partition.ID = uint32(id)
	partition.HashSlotStart = uint16(start)
	partition.HashSlotEnd = uint16(end)
	return partition, offset, nil
}

func appendDeliveryEnvelope(dst []byte, env runtimedelivery.Envelope) []byte {
	dst = appendUvarint(dst, env.MessageID)
	dst = appendUvarint(dst, env.MessageSeq)
	dst = appendString(dst, env.ChannelID)
	dst = append(dst, env.ChannelType)
	dst = appendString(dst, env.FromUID)
	dst = appendUvarint(dst, env.SenderNodeID)
	dst = appendUvarint(dst, env.SenderSessionID)
	dst = appendString(dst, env.ClientMsgNo)
	if env.RedDot {
		dst = append(dst, 1)
	} else {
		dst = append(dst, 0)
	}
	dst = appendBytes(dst, env.Payload)
	return appendStringSlice(dst, env.MessageScopedUIDs)
}

func readDeliveryEnvelope(body []byte, offset int) (runtimedelivery.Envelope, int, error) {
	var env runtimedelivery.Envelope
	var redDot byte
	var err error
	if env.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return runtimedelivery.Envelope{}, offset, err
	}
	if env.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return runtimedelivery.Envelope{}, offset, err
	}
	if env.ChannelID, offset, err = readString(body, offset); err != nil {
		return runtimedelivery.Envelope{}, offset, err
	}
	if env.ChannelType, offset, err = readByte(body, offset, "delivery channel type"); err != nil {
		return runtimedelivery.Envelope{}, offset, err
	}
	if env.FromUID, offset, err = readString(body, offset); err != nil {
		return runtimedelivery.Envelope{}, offset, err
	}
	if env.SenderNodeID, offset, err = readUvarint(body, offset); err != nil {
		return runtimedelivery.Envelope{}, offset, err
	}
	if env.SenderSessionID, offset, err = readUvarint(body, offset); err != nil {
		return runtimedelivery.Envelope{}, offset, err
	}
	if env.ClientMsgNo, offset, err = readString(body, offset); err != nil {
		return runtimedelivery.Envelope{}, offset, err
	}
	if redDot, offset, err = readByte(body, offset, "delivery red dot"); err != nil {
		return runtimedelivery.Envelope{}, offset, err
	}
	switch redDot {
	case 0:
		env.RedDot = false
	case 1:
		env.RedDot = true
	default:
		return runtimedelivery.Envelope{}, offset, fmt.Errorf("internal/access/node: invalid delivery red dot flag")
	}
	if env.Payload, offset, err = readBytes(body, offset); err != nil {
		return runtimedelivery.Envelope{}, offset, err
	}
	if env.MessageScopedUIDs, offset, err = readStringSlice(body, offset, "delivery message scoped uids"); err != nil {
		return runtimedelivery.Envelope{}, offset, err
	}
	return env, offset, nil
}

func appendDeliveryPushResult(dst []byte, result runtimedelivery.PushResult) []byte {
	dst = appendDeliveryRoutes(dst, result.Accepted)
	dst = appendDeliveryRoutes(dst, result.Retryable)
	return appendDeliveryRoutes(dst, result.Dropped)
}

func readDeliveryPushResult(body []byte, offset int) (runtimedelivery.PushResult, int, error) {
	var result runtimedelivery.PushResult
	var err error
	if result.Accepted, offset, err = readDeliveryRoutes(body, offset); err != nil {
		return runtimedelivery.PushResult{}, offset, err
	}
	if result.Retryable, offset, err = readDeliveryRoutes(body, offset); err != nil {
		return runtimedelivery.PushResult{}, offset, err
	}
	if result.Dropped, offset, err = readDeliveryRoutes(body, offset); err != nil {
		return runtimedelivery.PushResult{}, offset, err
	}
	return result, offset, nil
}

func appendDeliveryRoutes(dst []byte, routes []runtimedelivery.Route) []byte {
	dst = appendUvarint(dst, uint64(len(routes)))
	for _, route := range routes {
		dst = appendDeliveryRoute(dst, route)
	}
	return dst
}

func readDeliveryRoutes(body []byte, offset int) ([]runtimedelivery.Route, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateDeliveryCollectionLen(count, len(body)-offset, "delivery routes"); err != nil {
		return nil, offset, err
	}
	routes := make([]runtimedelivery.Route, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var route runtimedelivery.Route
		if route, offset, err = readDeliveryRoute(body, offset); err != nil {
			return nil, offset, err
		}
		routes = append(routes, route)
	}
	return routes, offset, nil
}

func appendDeliveryRoute(dst []byte, route runtimedelivery.Route) []byte {
	dst = appendString(dst, route.UID)
	dst = appendUvarint(dst, route.OwnerNodeID)
	dst = appendUvarint(dst, route.OwnerBootID)
	dst = appendUvarint(dst, route.OwnerSeq)
	dst = appendUvarint(dst, route.SessionID)
	dst = appendString(dst, route.DeviceID)
	dst = append(dst, route.DeviceFlag, route.DeviceLevel)
	return dst
}

func readDeliveryRoute(body []byte, offset int) (runtimedelivery.Route, int, error) {
	var route runtimedelivery.Route
	var err error
	if route.UID, offset, err = readString(body, offset); err != nil {
		return runtimedelivery.Route{}, offset, err
	}
	if route.OwnerNodeID, offset, err = readUvarint(body, offset); err != nil {
		return runtimedelivery.Route{}, offset, err
	}
	if route.OwnerBootID, offset, err = readUvarint(body, offset); err != nil {
		return runtimedelivery.Route{}, offset, err
	}
	if route.OwnerSeq, offset, err = readUvarint(body, offset); err != nil {
		return runtimedelivery.Route{}, offset, err
	}
	if route.SessionID, offset, err = readUvarint(body, offset); err != nil {
		return runtimedelivery.Route{}, offset, err
	}
	if route.DeviceID, offset, err = readString(body, offset); err != nil {
		return runtimedelivery.Route{}, offset, err
	}
	if route.DeviceFlag, offset, err = readByte(body, offset, "delivery device flag"); err != nil {
		return runtimedelivery.Route{}, offset, err
	}
	if route.DeviceLevel, offset, err = readByte(body, offset, "delivery device level"); err != nil {
		return runtimedelivery.Route{}, offset, err
	}
	return route, offset, nil
}

func appendBytes(dst []byte, value []byte) []byte {
	dst = appendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func readBytes(body []byte, offset int) ([]byte, int, error) {
	n, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if n > uint64(len(body)-offset) {
		return nil, offset, fmt.Errorf("internal/access/node: short bytes")
	}
	if n == 0 {
		return nil, offset, nil
	}
	end := offset + int(n)
	return append([]byte(nil), body[offset:end]...), end, nil
}

func appendStringSlice(dst []byte, values []string) []byte {
	dst = appendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = appendString(dst, value)
	}
	return dst
}

func readStringSlice(body []byte, offset int, label string) ([]string, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateDeliveryCollectionLen(count, len(body)-offset, label); err != nil {
		return nil, offset, err
	}
	values := make([]string, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var value string
		if value, offset, err = readString(body, offset); err != nil {
			return nil, offset, err
		}
		values = append(values, value)
	}
	return values, offset, nil
}

func validateDeliveryCollectionLen(count uint64, remaining int, label string) error {
	if count > uint64(remaining) {
		return fmt.Errorf("internal/access/node: %s length exceeds payload", label)
	}
	if count > maxDeliveryRPCCollectionLen {
		return fmt.Errorf("internal/access/node: %s length exceeds limit", label)
	}
	return nil
}
