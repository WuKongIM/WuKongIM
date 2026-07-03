package node

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/legacy/contracts/messageevents"
)

var (
	pluginCommittedRequestMagic  = [...]byte{'W', 'K', 'P', 'C', 1}
	pluginCommittedResponseMagic = [...]byte{'W', 'K', 'P', 'D', 1}
)

const (
	maxPluginCommittedRequestBytes  = 10<<20 + 64<<10
	maxPluginCommittedResponseBytes = 64 << 10
	maxPluginCommittedMessageUIDs   = 10000
)

func encodePluginCommittedRequest(req pluginCommittedRequest) ([]byte, error) {
	dst := make([]byte, 0, len(pluginCommittedRequestMagic)+128+len(req.Event.Message.Payload))
	dst = append(dst, pluginCommittedRequestMagic[:]...)
	dst = appendPluginCommittedEvent(dst, req.Event)
	if len(dst) > maxPluginCommittedRequestBytes {
		return nil, fmt.Errorf("access/node: plugin committed request too large")
	}
	return dst, nil
}

func decodePluginCommittedRequest(body []byte) (pluginCommittedRequest, error) {
	if len(body) > maxPluginCommittedRequestBytes {
		return pluginCommittedRequest{}, fmt.Errorf("access/node: plugin committed request too large")
	}
	if !hasMagic(body, pluginCommittedRequestMagic[:]) {
		return pluginCommittedRequest{}, fmt.Errorf("access/node: invalid plugin committed request codec")
	}
	event, next, err := readPluginCommittedEvent(body, len(pluginCommittedRequestMagic))
	if err != nil {
		return pluginCommittedRequest{}, err
	}
	if next != len(body) {
		return pluginCommittedRequest{}, fmt.Errorf("access/node: trailing plugin committed request bytes")
	}
	return pluginCommittedRequest{Event: event}, nil
}

func encodePluginCommittedResponse(resp pluginCommittedResponse) ([]byte, error) {
	dst := make([]byte, 0, len(pluginCommittedResponseMagic)+len(resp.Status)+len(resp.Error)+8)
	dst = append(dst, pluginCommittedResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendString(dst, resp.Error)
	if len(dst) > maxPluginCommittedResponseBytes {
		return nil, fmt.Errorf("access/node: plugin committed response too large")
	}
	return dst, nil
}

func decodePluginCommittedResponse(body []byte) (pluginCommittedResponse, error) {
	if len(body) > maxPluginCommittedResponseBytes {
		return pluginCommittedResponse{}, fmt.Errorf("access/node: plugin committed response too large")
	}
	if !hasMagic(body, pluginCommittedResponseMagic[:]) {
		return pluginCommittedResponse{}, fmt.Errorf("access/node: invalid plugin committed response codec")
	}
	status, next, err := readString(body, len(pluginCommittedResponseMagic))
	if err != nil {
		return pluginCommittedResponse{}, err
	}
	errText, next, err := readString(body, next)
	if err != nil {
		return pluginCommittedResponse{}, err
	}
	if next != len(body) {
		return pluginCommittedResponse{}, fmt.Errorf("access/node: trailing plugin committed response bytes")
	}
	return pluginCommittedResponse{Status: status, Error: errText}, nil
}

func appendPluginCommittedEvent(dst []byte, event messageevents.MessageCommitted) []byte {
	dst = appendChannelMessage(dst, event.Message)
	dst = appendUvarint(dst, event.SenderSessionID)
	dst = appendStrings(dst, event.MessageScopedUIDs)
	return appendPluginCommittedBool(dst, event.CMDConversationIntentSubmitted)
}

func readPluginCommittedEvent(body []byte, offset int) (messageevents.MessageCommitted, int, error) {
	var event messageevents.MessageCommitted
	var err error
	if event.Message, offset, err = readChannelMessage(body, offset); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.SenderSessionID, offset, err = readUvarint(body, offset); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.MessageScopedUIDs, offset, err = readPluginCommittedMessageUIDs(body, offset); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	if event.CMDConversationIntentSubmitted, offset, err = readPluginCommittedBool(body, offset); err != nil {
		return messageevents.MessageCommitted{}, offset, err
	}
	return event, offset, nil
}

func readPluginCommittedMessageUIDs(body []byte, offset int) ([]string, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if count > maxPluginCommittedMessageUIDs {
		return nil, offset, fmt.Errorf("access/node: plugin committed message scoped uids exceeds limit")
	}
	offset = next
	valuesLen, err := readCollectionLen(count, len(body)-offset, "plugin committed message scoped uids")
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

func appendPluginCommittedBool(dst []byte, v bool) []byte {
	if v {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func readPluginCommittedBool(body []byte, offset int) (bool, int, error) {
	if offset >= len(body) {
		return false, offset, fmt.Errorf("access/node: short plugin committed bool")
	}
	return body[offset] != 0, offset + 1, nil
}
