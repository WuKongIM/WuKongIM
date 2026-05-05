package node

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/contracts/deliveryevents"
)

var (
	deliveryAckRequestMagic     = [...]byte{'W', 'K', 'D', 'A', 1}
	deliveryOfflineRequestMagic = [...]byte{'W', 'K', 'D', 'O', 1}
	deliveryResponseMagic       = [...]byte{'W', 'K', 'D', 'S', 1}
)

// encodeDeliveryAckRequestBinary encodes delivery acknowledgement notifications without JSON reflection.
func encodeDeliveryAckRequestBinary(req deliveryAckRequest) ([]byte, error) {
	dst := make([]byte, 0, len(deliveryAckRequestMagic)+len(req.Command.UID)+32)
	dst = append(dst, deliveryAckRequestMagic[:]...)
	dst = appendRouteAckEvent(dst, req.Command)
	return dst, nil
}

func decodeDeliveryAckRequest(body []byte) (deliveryAckRequest, error) {
	if !isDeliveryAckRequestBinary(body) {
		return deliveryAckRequest{}, fmt.Errorf("access/node: invalid delivery ack request codec")
	}
	offset := len(deliveryAckRequestMagic)
	cmd, next, err := readRouteAckEvent(body, offset)
	if err != nil {
		return deliveryAckRequest{}, err
	}
	if next != len(body) {
		return deliveryAckRequest{}, fmt.Errorf("access/node: trailing delivery ack request bytes")
	}
	return deliveryAckRequest{Command: cmd}, nil
}

// encodeDeliveryOfflineRequestBinary encodes delivery offline notifications without JSON reflection.
func encodeDeliveryOfflineRequestBinary(req deliveryOfflineRequest) ([]byte, error) {
	dst := make([]byte, 0, len(deliveryOfflineRequestMagic)+len(req.Command.UID)+16)
	dst = append(dst, deliveryOfflineRequestMagic[:]...)
	dst = appendSessionClosedEvent(dst, req.Command)
	return dst, nil
}

func decodeDeliveryOfflineRequest(body []byte) (deliveryOfflineRequest, error) {
	if !isDeliveryOfflineRequestBinary(body) {
		return deliveryOfflineRequest{}, fmt.Errorf("access/node: invalid delivery offline request codec")
	}
	offset := len(deliveryOfflineRequestMagic)
	cmd, next, err := readSessionClosedEvent(body, offset)
	if err != nil {
		return deliveryOfflineRequest{}, err
	}
	if next != len(body) {
		return deliveryOfflineRequest{}, fmt.Errorf("access/node: trailing delivery offline request bytes")
	}
	return deliveryOfflineRequest{Command: cmd}, nil
}

func encodeDeliveryResponseBinary(resp deliveryResponse) ([]byte, error) {
	dst := make([]byte, 0, len(deliveryResponseMagic)+len(resp.Status)+1)
	dst = append(dst, deliveryResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	return dst, nil
}

func decodeDeliveryResponseBinary(body []byte) (deliveryResponse, error) {
	if !isDeliveryResponseBinary(body) {
		return deliveryResponse{}, fmt.Errorf("access/node: invalid delivery response codec")
	}
	offset := len(deliveryResponseMagic)
	status, next, err := readString(body, offset)
	if err != nil {
		return deliveryResponse{}, err
	}
	if next != len(body) {
		return deliveryResponse{}, fmt.Errorf("access/node: trailing delivery response bytes")
	}
	return deliveryResponse{Status: status}, nil
}

func isDeliveryAckRequestBinary(body []byte) bool {
	return hasMagic(body, deliveryAckRequestMagic[:])
}

func isDeliveryOfflineRequestBinary(body []byte) bool {
	return hasMagic(body, deliveryOfflineRequestMagic[:])
}

func isDeliveryResponseBinary(body []byte) bool {
	return hasMagic(body, deliveryResponseMagic[:])
}

func appendRouteAckEvent(dst []byte, cmd deliveryevents.RouteAck) []byte {
	dst = appendString(dst, cmd.UID)
	dst = appendUvarint(dst, cmd.SessionID)
	dst = appendUvarint(dst, cmd.MessageID)
	dst = appendUvarint(dst, cmd.MessageSeq)
	return dst
}

func readRouteAckEvent(body []byte, offset int) (deliveryevents.RouteAck, int, error) {
	var cmd deliveryevents.RouteAck
	var err error
	if cmd.UID, offset, err = readString(body, offset); err != nil {
		return deliveryevents.RouteAck{}, offset, err
	}
	if cmd.SessionID, offset, err = readUvarint(body, offset); err != nil {
		return deliveryevents.RouteAck{}, offset, err
	}
	if cmd.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return deliveryevents.RouteAck{}, offset, err
	}
	if cmd.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return deliveryevents.RouteAck{}, offset, err
	}
	return cmd, offset, nil
}

func appendSessionClosedEvent(dst []byte, cmd deliveryevents.SessionClosed) []byte {
	dst = appendString(dst, cmd.UID)
	dst = appendUvarint(dst, cmd.SessionID)
	return dst
}

func readSessionClosedEvent(body []byte, offset int) (deliveryevents.SessionClosed, int, error) {
	var cmd deliveryevents.SessionClosed
	var err error
	if cmd.UID, offset, err = readString(body, offset); err != nil {
		return deliveryevents.SessionClosed{}, offset, err
	}
	if cmd.SessionID, offset, err = readUvarint(body, offset); err != nil {
		return deliveryevents.SessionClosed{}, offset, err
	}
	return cmd, offset, nil
}
