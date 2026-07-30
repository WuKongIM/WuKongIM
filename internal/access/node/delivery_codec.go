package node

import (
	"fmt"

	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
)

var (
	deliveryRPCRequestMagic  = [...]byte{'W', 'K', 'V', 'D', 1}
	deliveryRPCResponseMagic = [...]byte{'W', 'K', 'V', 'd', 1}
)

const maxDeliveryRPCCollectionLen = 4096

// deliveryPushRequest is the stable binary DTO for owner-node pushes.
type deliveryPushRequest struct {
	Command onlinedelivery.OwnerPush
}

// deliveryPushResponse is the stable binary DTO returned by owner-node pushes.
type deliveryPushResponse struct {
	Status string
	Result onlinedelivery.OwnerPushResult
}

func encodeDeliveryPushRequest(req deliveryPushRequest) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, deliveryRPCRequestMagic[:]...)
	dst = appendDeliveryOwnerPush(dst, req.Command)
	return dst, nil
}

func decodeDeliveryPushRequest(body []byte) (deliveryPushRequest, error) {
	if !hasMagic(body, deliveryRPCRequestMagic[:]) {
		return deliveryPushRequest{}, fmt.Errorf("internal/access/node: invalid delivery request codec")
	}
	command, offset, err := readDeliveryOwnerPush(body, len(deliveryRPCRequestMagic))
	if err != nil {
		return deliveryPushRequest{}, err
	}
	if offset != len(body) {
		return deliveryPushRequest{}, fmt.Errorf("internal/access/node: trailing delivery request bytes")
	}
	return deliveryPushRequest{Command: command}, nil
}

func encodeDeliveryPushResponse(resp deliveryPushResponse) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, deliveryRPCResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendDeliveryOwnerPushResult(dst, resp.Result)
	return dst, nil
}

func decodeDeliveryPushResponse(body []byte) (deliveryPushResponse, error) {
	if !hasMagic(body, deliveryRPCResponseMagic[:]) {
		return deliveryPushResponse{}, fmt.Errorf("internal/access/node: invalid delivery response codec")
	}
	status, offset, err := readString(body, len(deliveryRPCResponseMagic))
	if err != nil {
		return deliveryPushResponse{}, err
	}
	result, offset, err := readDeliveryOwnerPushResult(body, offset)
	if err != nil {
		return deliveryPushResponse{}, err
	}
	if offset != len(body) {
		return deliveryPushResponse{}, fmt.Errorf("internal/access/node: trailing delivery response bytes")
	}
	return deliveryPushResponse{Status: status, Result: result}, nil
}

func appendDeliveryOwnerPush(dst []byte, push onlinedelivery.OwnerPush) []byte {
	dst = appendUvarint(dst, push.OwnerNodeID)
	dst = appendDeliveryCommittedEnvelope(dst, push.Event)
	return appendDeliveryRoutes(dst, push.Routes)
}

func readDeliveryOwnerPush(body []byte, offset int) (onlinedelivery.OwnerPush, int, error) {
	var push onlinedelivery.OwnerPush
	var err error
	if push.OwnerNodeID, offset, err = readUvarint(body, offset); err != nil {
		return onlinedelivery.OwnerPush{}, offset, err
	}
	if push.Event, offset, err = readDeliveryCommittedEnvelope(body, offset); err != nil {
		return onlinedelivery.OwnerPush{}, offset, err
	}
	if push.Routes, offset, err = readDeliveryRoutes(body, offset); err != nil {
		return onlinedelivery.OwnerPush{}, offset, err
	}
	return push, offset, nil
}

// appendDeliveryCommittedEnvelope deliberately preserves the pre-convergence
// owner-push bytes for mixed-version clusters.
func appendDeliveryCommittedEnvelope(dst []byte, event channelappendcontract.CommittedEnvelope) []byte {
	dst = appendUvarint(dst, event.MessageID)
	dst = appendUvarint(dst, event.MessageSeq)
	dst = appendString(dst, event.ChannelID)
	dst = append(dst, event.ChannelType)
	dst = appendString(dst, event.FromUID)
	dst = appendUvarint(dst, event.SenderNodeID)
	dst = appendUvarint(dst, event.SenderSessionID)
	dst = appendString(dst, event.ClientMsgNo)
	if event.RedDot {
		dst = append(dst, 1)
	} else {
		dst = append(dst, 0)
	}
	dst = appendDeliveryBytes(dst, event.Payload)
	return appendDeliveryStringSlice(dst, event.MessageScopedUIDs)
}

func readDeliveryCommittedEnvelope(body []byte, offset int) (channelappendcontract.CommittedEnvelope, int, error) {
	var event channelappendcontract.CommittedEnvelope
	var redDot byte
	var err error
	if event.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return channelappendcontract.CommittedEnvelope{}, offset, err
	}
	if event.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return channelappendcontract.CommittedEnvelope{}, offset, err
	}
	if event.ChannelID, offset, err = readString(body, offset); err != nil {
		return channelappendcontract.CommittedEnvelope{}, offset, err
	}
	if event.ChannelType, offset, err = readByte(body, offset, "delivery channel type"); err != nil {
		return channelappendcontract.CommittedEnvelope{}, offset, err
	}
	if event.FromUID, offset, err = readString(body, offset); err != nil {
		return channelappendcontract.CommittedEnvelope{}, offset, err
	}
	if event.SenderNodeID, offset, err = readUvarint(body, offset); err != nil {
		return channelappendcontract.CommittedEnvelope{}, offset, err
	}
	if event.SenderSessionID, offset, err = readUvarint(body, offset); err != nil {
		return channelappendcontract.CommittedEnvelope{}, offset, err
	}
	if event.ClientMsgNo, offset, err = readString(body, offset); err != nil {
		return channelappendcontract.CommittedEnvelope{}, offset, err
	}
	if redDot, offset, err = readByte(body, offset, "delivery red dot"); err != nil {
		return channelappendcontract.CommittedEnvelope{}, offset, err
	}
	switch redDot {
	case 0:
	case 1:
		event.RedDot = true
	default:
		return channelappendcontract.CommittedEnvelope{}, offset, fmt.Errorf("internal/access/node: invalid delivery red dot flag")
	}
	if event.Payload, offset, err = readDeliveryBytes(body, offset); err != nil {
		return channelappendcontract.CommittedEnvelope{}, offset, err
	}
	if event.MessageScopedUIDs, offset, err = readDeliveryStringSlice(body, offset, "delivery message scoped uids"); err != nil {
		return channelappendcontract.CommittedEnvelope{}, offset, err
	}
	return event, offset, nil
}

func appendDeliveryOwnerPushResult(dst []byte, result onlinedelivery.OwnerPushResult) []byte {
	dst = appendDeliveryRoutes(dst, result.Accepted)
	dst = appendDeliveryRoutes(dst, result.Retryable)
	return appendDeliveryRoutes(dst, result.Dropped)
}

func readDeliveryOwnerPushResult(body []byte, offset int) (onlinedelivery.OwnerPushResult, int, error) {
	var result onlinedelivery.OwnerPushResult
	var err error
	if result.Accepted, offset, err = readDeliveryRoutes(body, offset); err != nil {
		return onlinedelivery.OwnerPushResult{}, offset, err
	}
	if result.Retryable, offset, err = readDeliveryRoutes(body, offset); err != nil {
		return onlinedelivery.OwnerPushResult{}, offset, err
	}
	if result.Dropped, offset, err = readDeliveryRoutes(body, offset); err != nil {
		return onlinedelivery.OwnerPushResult{}, offset, err
	}
	return result, offset, nil
}

func appendDeliveryRoutes(dst []byte, routes []onlinedelivery.Route) []byte {
	dst = appendUvarint(dst, uint64(len(routes)))
	for _, route := range routes {
		dst = appendDeliveryRoute(dst, route)
	}
	return dst
}

func readDeliveryRoutes(body []byte, offset int) ([]onlinedelivery.Route, int, error) {
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
	routes := make([]onlinedelivery.Route, 0, int(count))
	for i := uint64(0); i < count; i++ {
		route, nextOffset, err := readDeliveryRoute(body, offset)
		if err != nil {
			return nil, offset, err
		}
		routes = append(routes, route)
		offset = nextOffset
	}
	return routes, offset, nil
}

func appendDeliveryRoute(dst []byte, route onlinedelivery.Route) []byte {
	dst = appendString(dst, route.UID)
	dst = appendUvarint(dst, route.OwnerNodeID)
	dst = appendUvarint(dst, route.OwnerBootID)
	dst = appendUvarint(dst, route.OwnerSeq)
	dst = appendUvarint(dst, route.SessionID)
	dst = appendString(dst, route.DeviceID)
	dst = append(dst, route.DeviceFlag, route.DeviceLevel)
	return dst
}

func readDeliveryRoute(body []byte, offset int) (onlinedelivery.Route, int, error) {
	var route onlinedelivery.Route
	var err error
	if route.UID, offset, err = readString(body, offset); err != nil {
		return onlinedelivery.Route{}, offset, err
	}
	if route.OwnerNodeID, offset, err = readUvarint(body, offset); err != nil {
		return onlinedelivery.Route{}, offset, err
	}
	if route.OwnerBootID, offset, err = readUvarint(body, offset); err != nil {
		return onlinedelivery.Route{}, offset, err
	}
	if route.OwnerSeq, offset, err = readUvarint(body, offset); err != nil {
		return onlinedelivery.Route{}, offset, err
	}
	if route.SessionID, offset, err = readUvarint(body, offset); err != nil {
		return onlinedelivery.Route{}, offset, err
	}
	if route.DeviceID, offset, err = readString(body, offset); err != nil {
		return onlinedelivery.Route{}, offset, err
	}
	if route.DeviceFlag, offset, err = readByte(body, offset, "delivery device flag"); err != nil {
		return onlinedelivery.Route{}, offset, err
	}
	if route.DeviceLevel, offset, err = readByte(body, offset, "delivery device level"); err != nil {
		return onlinedelivery.Route{}, offset, err
	}
	return route, offset, nil
}

func appendDeliveryBytes(dst []byte, value []byte) []byte {
	dst = appendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendBytes(dst []byte, value []byte) []byte {
	return appendDeliveryBytes(dst, value)
}

func readDeliveryBytes(body []byte, offset int) ([]byte, int, error) {
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

func readBytes(body []byte, offset int) ([]byte, int, error) {
	return readDeliveryBytes(body, offset)
}

func appendDeliveryStringSlice(dst []byte, values []string) []byte {
	dst = appendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = appendString(dst, value)
	}
	return dst
}

func appendStringSlice(dst []byte, values []string) []byte {
	return appendDeliveryStringSlice(dst, values)
}

func readDeliveryStringSlice(body []byte, offset int, label string) ([]string, int, error) {
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
		value, nextOffset, err := readString(body, offset)
		if err != nil {
			return nil, offset, err
		}
		values = append(values, value)
		offset = nextOffset
	}
	return values, offset, nil
}

func readStringSlice(body []byte, offset int, label string) ([]string, int, error) {
	return readDeliveryStringSlice(body, offset, label)
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
