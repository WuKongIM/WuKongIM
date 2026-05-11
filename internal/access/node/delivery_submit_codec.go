package node

import (
	"fmt"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

var deliverySubmitRequestMagic = [...]byte{'W', 'K', 'D', 'C', 1}

const maxDeliverySubmitMessageScopedUIDs = 10000

// encodeDeliverySubmitRequestBinary encodes committed delivery envelopes without JSON reflection.
func encodeDeliverySubmitRequestBinary(req deliverySubmitRequest) ([]byte, error) {
	dst := make([]byte, 0, len(deliverySubmitRequestMagic)+128+len(req.Envelope.Payload))
	dst = append(dst, deliverySubmitRequestMagic[:]...)
	dst = appendCommittedEnvelope(dst, req.Envelope)
	return dst, nil
}

func decodeDeliverySubmitRequest(body []byte) (deliverySubmitRequest, error) {
	if !isDeliverySubmitRequestBinary(body) {
		return deliverySubmitRequest{}, fmt.Errorf("access/node: invalid delivery submit request codec")
	}
	offset := len(deliverySubmitRequestMagic)
	envelope, next, err := readCommittedEnvelope(body, offset)
	if err != nil {
		return deliverySubmitRequest{}, err
	}
	if next != len(body) {
		return deliverySubmitRequest{}, fmt.Errorf("access/node: trailing delivery submit request bytes")
	}
	return deliverySubmitRequest{Envelope: envelope}, nil
}

func isDeliverySubmitRequestBinary(body []byte) bool {
	return hasMagic(body, deliverySubmitRequestMagic[:])
}

func appendCommittedEnvelope(dst []byte, envelope deliveryruntime.CommittedEnvelope) []byte {
	dst = appendChannelMessage(dst, envelope.Message)
	dst = appendUvarint(dst, envelope.SenderSessionID)
	dst = appendStrings(dst, envelope.MessageScopedUIDs)
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
