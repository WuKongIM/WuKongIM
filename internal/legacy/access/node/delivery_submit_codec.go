package node

import (
	"fmt"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/legacy/runtime/delivery"
)

var (
	deliverySubmitRequestMagicV1    = [...]byte{'W', 'K', 'D', 'C', 1}
	deliverySubmitRequestMagicV2    = [...]byte{'W', 'K', 'D', 'C', 2}
	deliverySubmitRequestMagicV3    = [...]byte{'W', 'K', 'D', 'C', 3}
	deliverySubmitCapabilityMagic   = [...]byte{'W', 'K', 'D', 'Q', 1}
	deliverySubmitV3CapabilityMagic = [...]byte{'W', 'K', 'D', 'Q', 3}
	// deliverySubmitRequestMagic preserves the original v1 magic for legacy binary payload helpers.
	deliverySubmitRequestMagic = deliverySubmitRequestMagicV1
)

const maxDeliverySubmitMessageScopedUIDs = 10000

// encodeDeliverySubmitRequestBinary encodes committed delivery envelopes without JSON reflection.
func encodeDeliverySubmitRequestBinary(req deliverySubmitRequest) ([]byte, error) {
	magic := deliverySubmitRequestMagicV1[:]
	includeScoped := len(req.Envelope.MessageScopedUIDs) > 0 || req.Envelope.CMDConversationIntentSubmitted
	includeIntentFlag := req.Envelope.CMDConversationIntentSubmitted
	if len(req.Envelope.MessageScopedUIDs) > 0 {
		magic = deliverySubmitRequestMagicV2[:]
	}
	if includeIntentFlag {
		magic = deliverySubmitRequestMagicV3[:]
	}
	dst := make([]byte, 0, len(magic)+128+len(req.Envelope.Payload))
	dst = append(dst, magic...)
	dst = appendCommittedEnvelope(dst, req.Envelope, includeScoped, includeIntentFlag)
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
	case hasMagic(body, deliverySubmitRequestMagicV3[:]):
		offset = len(deliverySubmitRequestMagicV3)
		envelope, next, err = readCommittedEnvelope(body, offset, true)
	case hasMagic(body, deliverySubmitRequestMagicV2[:]):
		offset = len(deliverySubmitRequestMagicV2)
		envelope, next, err = readCommittedEnvelope(body, offset, false)
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
	return hasMagic(body, deliverySubmitRequestMagicV1[:]) ||
		hasMagic(body, deliverySubmitRequestMagicV2[:]) ||
		hasMagic(body, deliverySubmitRequestMagicV3[:])
}

func encodeDeliverySubmitCapabilityProbe() []byte {
	return append([]byte(nil), deliverySubmitCapabilityMagic[:]...)
}

func isDeliverySubmitCapabilityProbe(body []byte) bool {
	return hasMagic(body, deliverySubmitCapabilityMagic[:]) && len(body) == len(deliverySubmitCapabilityMagic)
}

func encodeDeliverySubmitV3CapabilityProbe() []byte {
	return append([]byte(nil), deliverySubmitV3CapabilityMagic[:]...)
}

func isDeliverySubmitV3CapabilityProbe(body []byte) bool {
	return hasMagic(body, deliverySubmitV3CapabilityMagic[:]) && len(body) == len(deliverySubmitV3CapabilityMagic)
}

func appendCommittedEnvelope(dst []byte, envelope deliveryruntime.CommittedEnvelope, includeScoped, includeIntentFlag bool) []byte {
	dst = appendChannelMessage(dst, envelope.Message)
	dst = appendUvarint(dst, envelope.SenderSessionID)
	if includeScoped {
		dst = appendStrings(dst, envelope.MessageScopedUIDs)
	}
	if includeIntentFlag {
		dst = appendDeliverySubmitBool(dst, envelope.CMDConversationIntentSubmitted)
	}
	return dst
}

func readCommittedEnvelope(body []byte, offset int, includeIntentFlag bool) (deliveryruntime.CommittedEnvelope, int, error) {
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
	if includeIntentFlag {
		if envelope.CMDConversationIntentSubmitted, offset, err = readDeliverySubmitBool(body, offset); err != nil {
			return deliveryruntime.CommittedEnvelope{}, offset, err
		}
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

func appendDeliverySubmitBool(dst []byte, v bool) []byte {
	if v {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func readDeliverySubmitBool(body []byte, offset int) (bool, int, error) {
	if offset >= len(body) {
		return false, offset, fmt.Errorf("access/node: short delivery submit bool")
	}
	return body[offset] != 0, offset + 1, nil
}
