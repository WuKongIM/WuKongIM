package node

import (
	"fmt"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

var (
	deliverySubmitRequestMagicV1 = [...]byte{'W', 'K', 'D', 'C', 1}
	deliverySubmitRequestMagicV2 = [...]byte{'W', 'K', 'D', 'C', 2}
	// deliverySubmitRequestMagic preserves the original v1 magic for legacy binary payload helpers.
	deliverySubmitRequestMagic = deliverySubmitRequestMagicV1
)

const maxDeliverySubmitMessageScopedUIDs = 10000

// encodeDeliverySubmitRequestBinary encodes committed delivery envelopes without JSON reflection.
func encodeDeliverySubmitRequestBinary(req deliverySubmitRequest) ([]byte, error) {
	magic := deliverySubmitRequestMagicV1[:]
	includeScoped := len(req.Envelope.MessageScopedUIDs) > 0
	if includeScoped {
		magic = deliverySubmitRequestMagicV2[:]
	}
	dst := make([]byte, 0, len(magic)+128+len(req.Envelope.Payload))
	dst = append(dst, magic...)
	dst = appendCommittedEnvelope(dst, req.Envelope, includeScoped)
	return dst, nil
}

func decodeDeliverySubmitRequest(body []byte) (deliverySubmitRequest, error) {
	if !isDeliverySubmitRequestBinary(body) {
		return deliverySubmitRequest{}, fmt.Errorf("access/node: invalid delivery submit request codec")
	}
	var (
		offset   int
		legacy   bool
		envelope deliveryruntime.CommittedEnvelope
		next     int
		err      error
	)
	switch {
	case hasMagic(body, deliverySubmitRequestMagicV2[:]):
		offset = len(deliverySubmitRequestMagicV2)
		envelope, next, err = readCommittedEnvelope(body, offset)
	case hasMagic(body, deliverySubmitRequestMagicV1[:]):
		offset = len(deliverySubmitRequestMagicV1)
		envelope, next, err = readLegacyCommittedEnvelope(body, offset)
		legacy = true
	default:
		return deliverySubmitRequest{}, fmt.Errorf("access/node: invalid delivery submit request codec")
	}
	if err != nil {
		return deliverySubmitRequest{}, err
	}
	if next != len(body) {
		return deliverySubmitRequest{}, fmt.Errorf("access/node: trailing delivery submit request bytes")
	}
	if legacy {
		envelope.MessageScopedUIDs = nil
	}
	return deliverySubmitRequest{Envelope: envelope}, nil
}

func isDeliverySubmitRequestBinary(body []byte) bool {
	return hasMagic(body, deliverySubmitRequestMagicV1[:]) || hasMagic(body, deliverySubmitRequestMagicV2[:])
}

func appendCommittedEnvelope(dst []byte, envelope deliveryruntime.CommittedEnvelope, includeScoped bool) []byte {
	dst = appendChannelMessage(dst, envelope.Message)
	dst = appendUvarint(dst, envelope.SenderSessionID)
	if includeScoped {
		dst = appendStrings(dst, envelope.MessageScopedUIDs)
	}
	return dst
}

func readCommittedEnvelope(body []byte, offset int) (deliveryruntime.CommittedEnvelope, int, error) {
	var envelope deliveryruntime.CommittedEnvelope
	var err error
	if envelope.Message, offset, err = readChannelMessage(body, offset); err != nil {
		return deliveryruntime.CommittedEnvelope{}, offset, err
	}
	if envelope.SenderSessionID, offset, err = readUvarint(body, offset); err != nil {
		return deliveryruntime.CommittedEnvelope{}, offset, err
	}
	if envelope.MessageScopedUIDs, offset, err = readDeliverySubmitMessageScopedUIDs(body, offset); err != nil {
		return deliveryruntime.CommittedEnvelope{}, offset, err
	}
	return envelope, offset, nil
}

func readLegacyCommittedEnvelope(body []byte, offset int) (deliveryruntime.CommittedEnvelope, int, error) {
	var envelope deliveryruntime.CommittedEnvelope
	var err error
	if envelope.Message, offset, err = readChannelMessage(body, offset); err != nil {
		return deliveryruntime.CommittedEnvelope{}, offset, err
	}
	if envelope.SenderSessionID, offset, err = readUvarint(body, offset); err != nil {
		return deliveryruntime.CommittedEnvelope{}, offset, err
	}
	return envelope, offset, nil
}

func readDeliverySubmitMessageScopedUIDs(body []byte, offset int) ([]string, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if count > maxDeliverySubmitMessageScopedUIDs {
		return nil, offset, fmt.Errorf("access/node: message scoped uids exceeds limit")
	}
	offset = next
	valuesLen, err := readCollectionLen(count, len(body)-offset, "message scoped uids")
	if err != nil {
		return nil, offset, err
	}
	values := make([]string, valuesLen)
	for i := range values {
		if values[i], offset, err = readString(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return values, offset, nil
}
