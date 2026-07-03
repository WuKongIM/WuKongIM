package node

import (
	"encoding/binary"
	"fmt"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/legacy/runtime/delivery"
)

var (
	deliveryPushRequestMagicV1  = [...]byte{'W', 'K', 'D', 'P', 1}
	deliveryPushRequestMagicV2  = [...]byte{'W', 'K', 'D', 'P', 2}
	deliveryPushResponseMagicV1 = [...]byte{'W', 'K', 'D', 'R', 1}
	deliveryPushResponseMagicV2 = [...]byte{'W', 'K', 'D', 'R', 2}

	deliveryPushRequestMagic  = deliveryPushRequestMagicV1
	deliveryPushResponseMagic = deliveryPushResponseMagicV1
)

// encodeDeliveryPushCommandBinary encodes the legacy single-item push shape as binary.
func encodeDeliveryPushCommandBinary(cmd DeliveryPushCommand) ([]byte, error) {
	return encodeDeliveryPushRequestBinary(deliveryPushRequest{
		OwnerNodeID: cmd.OwnerNodeID,
		ChannelID:   cmd.ChannelID,
		ChannelType: cmd.ChannelType,
		MessageID:   cmd.MessageID,
		MessageSeq:  cmd.MessageSeq,
		Routes:      cmd.Routes,
		Frame:       cmd.Frame,
	}), nil
}

// encodeDeliveryPushBatchCommandBinary encodes batched delivery push RPCs as binary.
func encodeDeliveryPushBatchCommandBinary(cmd DeliveryPushBatchCommand) ([]byte, error) {
	return encodeDeliveryPushRequestBinary(deliveryPushRequest{
		OwnerNodeID:          cmd.OwnerNodeID,
		Items:                cmd.Items,
		acceptsAcceptedCount: true,
	}), nil
}

func encodeDeliveryPushBatchCommandLegacyBinary(cmd DeliveryPushBatchCommand) ([]byte, error) {
	return encodeDeliveryPushRequestBinary(deliveryPushRequest{
		OwnerNodeID: cmd.OwnerNodeID,
		Items:       cmd.Items,
	}), nil
}

func encodeDeliveryPushRequestBinary(req deliveryPushRequest) []byte {
	items := req.deliveryPushItems()
	dst := make([]byte, 0, estimateDeliveryPushRequestBinarySize(req.OwnerNodeID, items))
	if req.acceptsAcceptedCount {
		dst = append(dst, deliveryPushRequestMagicV2[:]...)
	} else {
		dst = append(dst, deliveryPushRequestMagicV1[:]...)
	}
	dst = appendUvarint(dst, req.OwnerNodeID)
	dst = appendUvarint(dst, uint64(len(items)))
	for _, item := range items {
		dst = appendDeliveryPushItem(dst, item)
	}
	return dst
}

func decodeDeliveryPushRequest(body []byte) (deliveryPushRequest, bool, error) {
	if !isDeliveryPushRequestBinary(body) {
		return deliveryPushRequest{}, false, fmt.Errorf("access/node: invalid delivery push request codec")
	}
	req, err := decodeDeliveryPushRequestBinary(body[len(deliveryPushRequestMagicV1):])
	if hasMagic(body, deliveryPushRequestMagicV2[:]) {
		req.acceptsAcceptedCount = true
	}
	return req, true, err
}

func decodeDeliveryPushRequestBinary(body []byte) (deliveryPushRequest, error) {
	var req deliveryPushRequest
	offset := 0
	var err error
	if req.OwnerNodeID, offset, err = readUvarint(body, offset); err != nil {
		return deliveryPushRequest{}, err
	}
	itemCount, next, err := readUvarint(body, offset)
	if err != nil {
		return deliveryPushRequest{}, err
	}
	offset = next
	itemsLen, err := readCollectionLen(itemCount, len(body)-offset, "delivery push items")
	if err != nil {
		return deliveryPushRequest{}, err
	}
	req.Items = make([]DeliveryPushItem, itemsLen)
	for i := range req.Items {
		if req.Items[i], offset, err = readDeliveryPushItem(body, offset); err != nil {
			return deliveryPushRequest{}, err
		}
	}
	if offset != len(body) {
		return deliveryPushRequest{}, fmt.Errorf("access/node: trailing delivery push request bytes")
	}
	return req, nil
}

func encodeDeliveryPushResponseBinary(resp DeliveryPushResponse) ([]byte, error) {
	dst := make([]byte, 0, estimateDeliveryPushResponseBinarySize(resp))
	if resp.AcceptedCountSet {
		dst = append(dst, deliveryPushResponseMagicV2[:]...)
	} else {
		dst = append(dst, deliveryPushResponseMagicV1[:]...)
	}
	dst = appendString(dst, resp.Status)
	if resp.AcceptedCountSet {
		dst = appendUvarint(dst, resp.AcceptedCount)
	} else {
		dst = appendRouteKeys(dst, resp.Accepted)
	}
	dst = appendRouteKeys(dst, resp.Retryable)
	dst = appendRouteKeys(dst, resp.Dropped)
	return dst, nil
}

func decodeDeliveryPushResponseBinary(body []byte) (DeliveryPushResponse, error) {
	responseV2 := hasMagic(body, deliveryPushResponseMagicV2[:])
	offset := len(deliveryPushResponseMagicV1)
	var resp DeliveryPushResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return DeliveryPushResponse{}, err
	}
	if responseV2 {
		if resp.AcceptedCount, offset, err = readUvarint(body, offset); err != nil {
			return DeliveryPushResponse{}, err
		}
		resp.AcceptedCountSet = true
	} else {
		if resp.Accepted, offset, err = readRouteKeys(body, offset); err != nil {
			return DeliveryPushResponse{}, err
		}
	}
	if resp.Retryable, offset, err = readRouteKeys(body, offset); err != nil {
		return DeliveryPushResponse{}, err
	}
	if resp.Dropped, offset, err = readRouteKeys(body, offset); err != nil {
		return DeliveryPushResponse{}, err
	}
	if offset != len(body) {
		return DeliveryPushResponse{}, fmt.Errorf("access/node: trailing delivery push response bytes")
	}
	return resp, nil
}

func isDeliveryPushRequestBinary(body []byte) bool {
	return hasMagic(body, deliveryPushRequestMagicV1[:]) || hasMagic(body, deliveryPushRequestMagicV2[:])
}

func isDeliveryPushResponseBinary(body []byte) bool {
	return hasMagic(body, deliveryPushResponseMagicV1[:]) || hasMagic(body, deliveryPushResponseMagicV2[:])
}

func appendDeliveryPushItem(dst []byte, item DeliveryPushItem) []byte {
	dst = appendString(dst, item.ChannelID)
	dst = append(dst, item.ChannelType)
	dst = appendUvarint(dst, item.MessageID)
	dst = appendUvarint(dst, item.MessageSeq)
	dst = appendRouteKeys(dst, item.Routes)
	dst = appendBytes(dst, item.Frame)
	return dst
}

func readDeliveryPushItem(body []byte, offset int) (DeliveryPushItem, int, error) {
	var item DeliveryPushItem
	var err error
	if item.ChannelID, offset, err = readString(body, offset); err != nil {
		return DeliveryPushItem{}, offset, err
	}
	if offset >= len(body) {
		return DeliveryPushItem{}, offset, fmt.Errorf("access/node: short delivery push channel type")
	}
	item.ChannelType = body[offset]
	offset++
	if item.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return DeliveryPushItem{}, offset, err
	}
	if item.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return DeliveryPushItem{}, offset, err
	}
	if item.Routes, offset, err = readRouteKeys(body, offset); err != nil {
		return DeliveryPushItem{}, offset, err
	}
	if item.Frame, offset, err = readBytes(body, offset); err != nil {
		return DeliveryPushItem{}, offset, err
	}
	return item, offset, nil
}

func appendRouteKeys(dst []byte, routes []deliveryruntime.RouteKey) []byte {
	dst = appendUvarint(dst, uint64(len(routes)))
	for _, route := range routes {
		dst = appendString(dst, route.UID)
		dst = appendUvarint(dst, route.NodeID)
		dst = appendUvarint(dst, route.BootID)
		dst = appendUvarint(dst, route.SessionID)
	}
	return dst
}

func readRouteKeys(body []byte, offset int) ([]deliveryruntime.RouteKey, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	routesLen, err := readCollectionLen(count, len(body)-offset, "delivery push routes")
	if err != nil {
		return nil, offset, err
	}
	routes := make([]deliveryruntime.RouteKey, routesLen)
	for i := range routes {
		if routes[i].UID, offset, err = readString(body, offset); err != nil {
			return nil, offset, err
		}
		if routes[i].NodeID, offset, err = readUvarint(body, offset); err != nil {
			return nil, offset, err
		}
		if routes[i].BootID, offset, err = readUvarint(body, offset); err != nil {
			return nil, offset, err
		}
		if routes[i].SessionID, offset, err = readUvarint(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return routes, offset, nil
}

func appendUvarint(dst []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append(dst, buf[:n]...)
}

func appendString(dst []byte, v string) []byte {
	dst = appendUvarint(dst, uint64(len(v)))
	return append(dst, v...)
}

func appendBytes(dst []byte, v []byte) []byte {
	dst = appendUvarint(dst, uint64(len(v)))
	return append(dst, v...)
}

func readUvarint(body []byte, offset int) (uint64, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("access/node: short uvarint")
	}
	v, n := binary.Uvarint(body[offset:])
	if n <= 0 {
		return 0, offset, fmt.Errorf("access/node: invalid uvarint")
	}
	return v, offset + n, nil
}

func readString(body []byte, offset int) (string, int, error) {
	raw, next, err := readBytes(body, offset)
	if err != nil {
		return "", offset, err
	}
	return string(raw), next, nil
}

func readBytes(body []byte, offset int) ([]byte, int, error) {
	length, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	end := offset + int(length)
	if end < offset || end > len(body) {
		return nil, offset, fmt.Errorf("access/node: short bytes")
	}
	return body[offset:end], end, nil
}

func readCollectionLen(count uint64, remaining int, label string) (int, error) {
	maxInt := uint64(^uint(0) >> 1)
	if count > maxInt {
		return 0, fmt.Errorf("access/node: %s count overflows int", label)
	}
	if count > uint64(remaining) {
		return 0, fmt.Errorf("access/node: %s count exceeds remaining bytes", label)
	}
	return int(count), nil
}

func hasMagic(body []byte, magic []byte) bool {
	if len(body) < len(magic) {
		return false
	}
	for i := range magic {
		if body[i] != magic[i] {
			return false
		}
	}
	return true
}

func estimateDeliveryPushRequestBinarySize(ownerNodeID uint64, items []DeliveryPushItem) int {
	size := len(deliveryPushRequestMagicV1) + binary.MaxVarintLen64 + binary.MaxVarintLen64
	_ = ownerNodeID
	for _, item := range items {
		size += len(item.ChannelID) + 1 + 1 + binary.MaxVarintLen64*3 + len(item.Frame)
		size += estimateRouteKeysBinarySize(item.Routes)
	}
	return size
}

func estimateDeliveryPushResponseBinarySize(resp DeliveryPushResponse) int {
	size := len(deliveryPushResponseMagicV1) + len(resp.Status) + 1 +
		estimateRouteKeysBinarySize(resp.Retryable) +
		estimateRouteKeysBinarySize(resp.Dropped)
	if resp.AcceptedCountSet {
		return size + binary.MaxVarintLen64
	}
	return size + estimateRouteKeysBinarySize(resp.Accepted)
}

func estimateRouteKeysBinarySize(routes []deliveryruntime.RouteKey) int {
	size := binary.MaxVarintLen64
	for _, route := range routes {
		size += len(route.UID) + 1 + binary.MaxVarintLen64*3
	}
	return size
}
