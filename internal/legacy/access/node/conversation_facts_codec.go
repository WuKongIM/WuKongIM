package node

import "fmt"

var (
	conversationFactsRequestMagic  = [...]byte{'W', 'K', 'C', 'F', 1}
	conversationFactsResponseMagic = [...]byte{'W', 'K', 'C', 'G', 1}
)

// encodeConversationFactsRequestBinary encodes conversation fact lookups without JSON reflection.
func encodeConversationFactsRequestBinary(req conversationFactsRequest) ([]byte, error) {
	dst := make([]byte, 0, len(conversationFactsRequestMagic)+128+len(req.Op))
	dst = append(dst, conversationFactsRequestMagic[:]...)
	dst = appendString(dst, req.Op)
	dst = appendConversationFactsChannelKey(dst, req.Key)
	dst = appendConversationFactsChannelKeys(dst, req.Keys)
	dst = appendNodeInt(dst, req.Limit)
	dst = appendNodeInt(dst, req.MaxBytes)
	return dst, nil
}

func decodeConversationFactsRequest(body []byte) (conversationFactsRequest, error) {
	if !isConversationFactsRequestBinary(body) {
		return conversationFactsRequest{}, fmt.Errorf("access/node: invalid conversation facts request codec")
	}
	offset := len(conversationFactsRequestMagic)
	var req conversationFactsRequest
	var err error
	if req.Op, offset, err = readString(body, offset); err != nil {
		return conversationFactsRequest{}, err
	}
	if req.Key, offset, err = readConversationFactsChannelKey(body, offset); err != nil {
		return conversationFactsRequest{}, err
	}
	if req.Keys, offset, err = readConversationFactsChannelKeys(body, offset); err != nil {
		return conversationFactsRequest{}, err
	}
	if req.Limit, offset, err = readNodeInt(body, offset, "conversation facts limit"); err != nil {
		return conversationFactsRequest{}, err
	}
	if req.MaxBytes, offset, err = readNodeInt(body, offset, "conversation facts max bytes"); err != nil {
		return conversationFactsRequest{}, err
	}
	if offset != len(body) {
		return conversationFactsRequest{}, fmt.Errorf("access/node: trailing conversation facts request bytes")
	}
	return req, nil
}

func encodeConversationFactsResponseBinary(resp conversationFactsResponse) ([]byte, error) {
	dst := make([]byte, 0, len(conversationFactsResponseMagic)+128+estimateChannelMessagesBinarySize(resp.Messages)+estimateConversationFactsEntriesBinarySize(resp.Entries))
	dst = append(dst, conversationFactsResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendChannelMessageSlice(dst, resp.Messages)
	dst = appendConversationFactsEntries(dst, resp.Entries)
	return dst, nil
}

func decodeConversationFactsResponseBinary(body []byte) (conversationFactsResponse, error) {
	if !isConversationFactsResponseBinary(body) {
		return conversationFactsResponse{}, fmt.Errorf("access/node: invalid conversation facts response codec")
	}
	offset := len(conversationFactsResponseMagic)
	var resp conversationFactsResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return conversationFactsResponse{}, err
	}
	if resp.Messages, offset, err = readChannelMessageSlice(body, offset); err != nil {
		return conversationFactsResponse{}, err
	}
	if resp.Entries, offset, err = readConversationFactsEntries(body, offset); err != nil {
		return conversationFactsResponse{}, err
	}
	if offset != len(body) {
		return conversationFactsResponse{}, fmt.Errorf("access/node: trailing conversation facts response bytes")
	}
	return resp, nil
}

func isConversationFactsRequestBinary(body []byte) bool {
	return hasMagic(body, conversationFactsRequestMagic[:])
}

func isConversationFactsResponseBinary(body []byte) bool {
	return hasMagic(body, conversationFactsResponseMagic[:])
}

func appendConversationFactsChannelKey(dst []byte, key conversationFactsChannelKey) []byte {
	dst = appendString(dst, key.ID)
	return append(dst, key.Type)
}

func readConversationFactsChannelKey(body []byte, offset int) (conversationFactsChannelKey, int, error) {
	id, next, err := readString(body, offset)
	if err != nil {
		return conversationFactsChannelKey{}, offset, err
	}
	if next >= len(body) {
		return conversationFactsChannelKey{}, offset, fmt.Errorf("access/node: short conversation facts channel type")
	}
	return conversationFactsChannelKey{ID: id, Type: body[next]}, next + 1, nil
}

func appendConversationFactsChannelKeys(dst []byte, keys []conversationFactsChannelKey) []byte {
	if keys == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = appendUvarint(dst, uint64(len(keys)))
	for _, key := range keys {
		dst = appendConversationFactsChannelKey(dst, key)
	}
	return dst
}

func readConversationFactsChannelKeys(body []byte, offset int) ([]conversationFactsChannelKey, int, error) {
	marker, next, err := readNodeMarker(body, offset, "conversation facts channel keys")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	count, next, err := readUvarint(body, next)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	keysLen, err := readCollectionLen(count, len(body)-offset, "conversation facts channel keys")
	if err != nil {
		return nil, offset, err
	}
	keys := make([]conversationFactsChannelKey, keysLen)
	for i := range keys {
		if keys[i], offset, err = readConversationFactsChannelKey(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return keys, offset, nil
}

func appendConversationFactsEntries(dst []byte, entries []conversationFactsEntry) []byte {
	if entries == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = appendUvarint(dst, uint64(len(entries)))
	for _, entry := range entries {
		dst = appendConversationFactsChannelKey(dst, entry.Key)
		dst = appendChannelMessageSlice(dst, entry.Messages)
	}
	return dst
}

func readConversationFactsEntries(body []byte, offset int) ([]conversationFactsEntry, int, error) {
	marker, next, err := readNodeMarker(body, offset, "conversation facts entries")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	count, next, err := readUvarint(body, next)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	entriesLen, err := readCollectionLen(count, len(body)-offset, "conversation facts entries")
	if err != nil {
		return nil, offset, err
	}
	entries := make([]conversationFactsEntry, entriesLen)
	for i := range entries {
		if entries[i].Key, offset, err = readConversationFactsChannelKey(body, offset); err != nil {
			return nil, offset, err
		}
		if entries[i].Messages, offset, err = readChannelMessageSlice(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return entries, offset, nil
}

func estimateConversationFactsEntriesBinarySize(entries []conversationFactsEntry) int {
	size := 1 + binaryMaxVarintLen64()
	for _, entry := range entries {
		size += len(entry.Key.ID) + 1 + 1
		size += estimateChannelMessagesBinarySize(entry.Messages)
	}
	return size
}
