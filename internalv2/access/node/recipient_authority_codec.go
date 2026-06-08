package node

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
)

var (
	recipientAuthorityRequestMagic  = [...]byte{'W', 'K', 'V', 'A', 1}
	recipientAuthorityResponseMagic = [...]byte{'W', 'K', 'V', 'a', 1}
)

const maxRecipientAuthorityCollectionLen = 4096

// recipientAuthorityRequest is the deterministic binary DTO for recipient authority work.
type recipientAuthorityRequest struct {
	// Target fences the request to one observed recipient UID authority epoch.
	Target authority.Target
	// Event is the durable committed message being processed.
	Event messageevents.MessageCommitted
	// Recipients are the UIDs owned by Target for this request.
	Recipients []recipientusecase.Recipient
}

// recipientAuthorityResponse is the deterministic binary DTO returned by recipient authority calls.
type recipientAuthorityResponse struct {
	// Status is the envelope-level RPC status.
	Status string
}

func encodeRecipientAuthorityRequest(req recipientAuthorityRequest) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, recipientAuthorityRequestMagic[:]...)
	dst = appendAuthorityTarget(dst, req.Target)
	dst = appendMessageCommitted(dst, req.Event)
	dst = appendRecipientAuthorityRecipients(dst, req.Recipients)
	return dst, nil
}

func decodeRecipientAuthorityRequest(body []byte) (recipientAuthorityRequest, error) {
	if !hasMagic(body, recipientAuthorityRequestMagic[:]) {
		return recipientAuthorityRequest{}, fmt.Errorf("internalv2/access/node: invalid recipient authority request codec")
	}
	offset := len(recipientAuthorityRequestMagic)
	var req recipientAuthorityRequest
	var err error
	if req.Target, offset, err = readAuthorityTarget(body, offset); err != nil {
		return recipientAuthorityRequest{}, err
	}
	if req.Event, offset, err = readMessageCommitted(body, offset); err != nil {
		return recipientAuthorityRequest{}, err
	}
	if req.Recipients, offset, err = readRecipientAuthorityRecipients(body, offset); err != nil {
		return recipientAuthorityRequest{}, err
	}
	if offset != len(body) {
		return recipientAuthorityRequest{}, fmt.Errorf("internalv2/access/node: trailing recipient authority request bytes")
	}
	return req, nil
}

func encodeRecipientAuthorityResponse(resp recipientAuthorityResponse) ([]byte, error) {
	dst := make([]byte, 0, 32)
	dst = append(dst, recipientAuthorityResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	return dst, nil
}

func decodeRecipientAuthorityResponse(body []byte) (recipientAuthorityResponse, error) {
	if !hasMagic(body, recipientAuthorityResponseMagic[:]) {
		return recipientAuthorityResponse{}, fmt.Errorf("internalv2/access/node: invalid recipient authority response codec")
	}
	offset := len(recipientAuthorityResponseMagic)
	var resp recipientAuthorityResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return recipientAuthorityResponse{}, err
	}
	if offset != len(body) {
		return recipientAuthorityResponse{}, fmt.Errorf("internalv2/access/node: trailing recipient authority response bytes")
	}
	return resp, nil
}

func appendMessageCommitted(dst []byte, event messageevents.MessageCommitted) []byte {
	dst = appendUvarint(dst, event.MessageID)
	dst = appendUvarint(dst, event.MessageSeq)
	dst = appendString(dst, event.ChannelID)
	dst = append(dst, event.ChannelType)
	dst = appendString(dst, event.FromUID)
	dst = appendUvarint(dst, event.SenderNodeID)
	dst = appendUvarint(dst, event.SenderSessionID)
	dst = appendString(dst, event.ClientMsgNo)
	dst = appendVarint(dst, event.ServerTimestampMS)
	dst = appendBytes(dst, event.Payload)
	dst = appendRecipientAuthorityBool(dst, event.RedDot)
	return appendRecipientAuthorityStringSlice(dst, event.MessageScopedUIDs, "recipient authority scoped uids")
}

func readMessageCommitted(body []byte, offset int) (messageevents.MessageCommitted, int, error) {
	var event messageevents.MessageCommitted
	var err error
	if event.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.ChannelID, offset, err = readString(body, offset); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.ChannelType, offset, err = readByte(body, offset, "recipient authority channel type"); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.FromUID, offset, err = readString(body, offset); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.SenderNodeID, offset, err = readUvarint(body, offset); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.SenderSessionID, offset, err = readUvarint(body, offset); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.ClientMsgNo, offset, err = readString(body, offset); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.ServerTimestampMS, offset, err = readVarint(body, offset); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.Payload, offset, err = readBytes(body, offset); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.RedDot, offset, err = readRecipientAuthorityBool(body, offset, "recipient authority red dot"); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.MessageScopedUIDs, offset, err = readRecipientAuthorityStringSlice(body, offset, "recipient authority scoped uids"); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	return event, offset, nil
}

func appendRecipientAuthorityRecipients(dst []byte, recipients []recipientusecase.Recipient) []byte {
	dst = appendUvarint(dst, uint64(len(recipients)))
	for _, recipient := range recipients {
		dst = appendString(dst, recipient.UID)
		dst = appendUvarint(dst, recipient.JoinSeq)
	}
	return dst
}

func readRecipientAuthorityRecipients(body []byte, offset int) ([]recipientusecase.Recipient, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateRecipientAuthorityCollectionLen(count, len(body)-offset, "recipient authority recipients"); err != nil {
		return nil, offset, err
	}
	recipients := make([]recipientusecase.Recipient, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var recipient recipientusecase.Recipient
		if recipient.UID, offset, err = readString(body, offset); err != nil {
			return nil, offset, err
		}
		if recipient.JoinSeq, offset, err = readUvarint(body, offset); err != nil {
			return nil, offset, err
		}
		recipients = append(recipients, recipient)
	}
	return recipients, offset, nil
}

func appendRecipientAuthorityStringSlice(dst []byte, values []string, _ string) []byte {
	dst = appendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = appendString(dst, value)
	}
	return dst
}

func readRecipientAuthorityStringSlice(body []byte, offset int, label string) ([]string, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateRecipientAuthorityCollectionLen(count, len(body)-offset, label); err != nil {
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

func appendRecipientAuthorityBool(dst []byte, value bool) []byte {
	if value {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func readRecipientAuthorityBool(body []byte, offset int, label string) (bool, int, error) {
	v, next, err := readByte(body, offset, label)
	if err != nil {
		return false, offset, err
	}
	switch v {
	case 0:
		return false, next, nil
	case 1:
		return true, next, nil
	default:
		return false, offset, fmt.Errorf("internalv2/access/node: invalid %s flag", label)
	}
}

func validateRecipientAuthorityCollectionLen(count uint64, remaining int, label string) error {
	if count > maxRecipientAuthorityCollectionLen {
		return fmt.Errorf("internalv2/access/node: %s length exceeds limit", label)
	}
	if count > uint64(remaining) {
		return fmt.Errorf("internalv2/access/node: %s length exceeds payload", label)
	}
	return nil
}
