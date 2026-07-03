package node

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

var (
	channelMessagesRequestMagic  = [...]byte{'W', 'K', 'C', 'M', 1}
	channelMessagesResponseMagic = [...]byte{'W', 'K', 'C', 'N', 1}
)

// encodeChannelMessagesRequestBinary encodes channel message queries without JSON reflection.
func encodeChannelMessagesRequestBinary(req channelMessagesRequest) ([]byte, error) {
	dst := make([]byte, 0, len(channelMessagesRequestMagic)+128+len(req.Query.ClientMsgNo))
	dst = append(dst, channelMessagesRequestMagic[:]...)
	dst = appendChannelMessagesQuery(dst, req.Query)
	return dst, nil
}

func decodeChannelMessagesRequest(body []byte) (channelMessagesRequest, error) {
	if !isChannelMessagesRequestBinary(body) {
		return channelMessagesRequest{}, fmt.Errorf("access/node: invalid channel messages request codec")
	}
	offset := len(channelMessagesRequestMagic)
	query, next, err := readChannelMessagesQuery(body, offset)
	if err != nil {
		return channelMessagesRequest{}, err
	}
	if next != len(body) {
		return channelMessagesRequest{}, fmt.Errorf("access/node: trailing channel messages request bytes")
	}
	return channelMessagesRequest{Query: query}, nil
}

func encodeChannelMessagesResponseBinary(resp channelMessagesResponse) ([]byte, error) {
	dst := make([]byte, 0, len(channelMessagesResponseMagic)+128+estimateChannelMessagesBinarySize(resp.Page.Messages))
	dst = append(dst, channelMessagesResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendUvarint(dst, resp.LeaderID)
	dst = appendChannelMessagesPage(dst, resp.Page)
	return dst, nil
}

func decodeChannelMessagesResponseBinary(body []byte) (channelMessagesResponse, error) {
	if !isChannelMessagesResponseBinary(body) {
		return channelMessagesResponse{}, fmt.Errorf("access/node: invalid channel messages response codec")
	}
	offset := len(channelMessagesResponseMagic)
	var resp channelMessagesResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return channelMessagesResponse{}, err
	}
	if resp.LeaderID, offset, err = readUvarint(body, offset); err != nil {
		return channelMessagesResponse{}, err
	}
	if resp.Page, offset, err = readChannelMessagesPage(body, offset); err != nil {
		return channelMessagesResponse{}, err
	}
	if offset != len(body) {
		return channelMessagesResponse{}, fmt.Errorf("access/node: trailing channel messages response bytes")
	}
	return resp, nil
}

func isChannelMessagesRequestBinary(body []byte) bool {
	return hasMagic(body, channelMessagesRequestMagic[:])
}

func isChannelMessagesResponseBinary(body []byte) bool {
	return hasMagic(body, channelMessagesResponseMagic[:])
}

func appendChannelMessagesQuery(dst []byte, query ChannelMessagesQuery) []byte {
	dst = appendChannelID(dst, query.ChannelID)
	dst = appendNodeBool(dst, query.SyncMode)
	dst = appendNodeBool(dst, query.MaxSeqOnly)
	dst = appendUvarint(dst, query.MinAvailableSeq)
	dst = appendUvarint(dst, query.BeforeSeq)
	dst = appendUvarint(dst, query.StartSeq)
	dst = appendUvarint(dst, query.EndSeq)
	dst = appendNodeInt(dst, query.Limit)
	dst = append(dst, query.PullMode)
	dst = appendUvarint(dst, query.MessageID)
	dst = appendString(dst, query.ClientMsgNo)
	return dst
}

func readChannelMessagesQuery(body []byte, offset int) (ChannelMessagesQuery, int, error) {
	var query ChannelMessagesQuery
	var err error
	if query.ChannelID, offset, err = readChannelID(body, offset); err != nil {
		return ChannelMessagesQuery{}, offset, err
	}
	if query.SyncMode, offset, err = readNodeBool(body, offset); err != nil {
		return ChannelMessagesQuery{}, offset, err
	}
	if query.MaxSeqOnly, offset, err = readNodeBool(body, offset); err != nil {
		return ChannelMessagesQuery{}, offset, err
	}
	if query.MinAvailableSeq, offset, err = readUvarint(body, offset); err != nil {
		return ChannelMessagesQuery{}, offset, err
	}
	if query.BeforeSeq, offset, err = readUvarint(body, offset); err != nil {
		return ChannelMessagesQuery{}, offset, err
	}
	if query.StartSeq, offset, err = readUvarint(body, offset); err != nil {
		return ChannelMessagesQuery{}, offset, err
	}
	if query.EndSeq, offset, err = readUvarint(body, offset); err != nil {
		return ChannelMessagesQuery{}, offset, err
	}
	if query.Limit, offset, err = readNodeInt(body, offset, "channel messages limit"); err != nil {
		return ChannelMessagesQuery{}, offset, err
	}
	if query.PullMode, offset, err = readByte(body, offset, "channel messages pull mode"); err != nil {
		return ChannelMessagesQuery{}, offset, err
	}
	if query.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return ChannelMessagesQuery{}, offset, err
	}
	if query.ClientMsgNo, offset, err = readString(body, offset); err != nil {
		return ChannelMessagesQuery{}, offset, err
	}
	return query, offset, nil
}

func appendChannelMessagesPage(dst []byte, page ChannelMessagesPage) []byte {
	dst = appendChannelMessageSlice(dst, page.Messages)
	dst = appendNodeBool(dst, page.HasMore)
	dst = appendUvarint(dst, page.NextBeforeSeq)
	dst = appendUvarint(dst, page.MaxMessageSeq)
	return dst
}

func readChannelMessagesPage(body []byte, offset int) (ChannelMessagesPage, int, error) {
	var page ChannelMessagesPage
	var err error
	if page.Messages, offset, err = readChannelMessageSlice(body, offset); err != nil {
		return ChannelMessagesPage{}, offset, err
	}
	if page.HasMore, offset, err = readNodeBool(body, offset); err != nil {
		return ChannelMessagesPage{}, offset, err
	}
	if page.NextBeforeSeq, offset, err = readUvarint(body, offset); err != nil {
		return ChannelMessagesPage{}, offset, err
	}
	if page.MaxMessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return ChannelMessagesPage{}, offset, err
	}
	return page, offset, nil
}

func appendChannelMessageSlice(dst []byte, messages []channel.Message) []byte {
	if messages == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = appendUvarint(dst, uint64(len(messages)))
	for _, msg := range messages {
		dst = appendChannelMessage(dst, msg)
	}
	return dst
}

func readChannelMessageSlice(body []byte, offset int) ([]channel.Message, int, error) {
	marker, next, err := readNodeMarker(body, offset, "channel message list")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	count, next, err := readUvarint(body, next)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	messagesLen, err := readCollectionLen(count, len(body)-offset, "channel message list")
	if err != nil {
		return nil, offset, err
	}
	messages := make([]channel.Message, messagesLen)
	for i := range messages {
		if messages[i], offset, err = readChannelMessage(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return messages, offset, nil
}

func estimateChannelMessagesBinarySize(messages []channel.Message) int {
	size := 1 + binaryMaxVarintLen64()
	for _, msg := range messages {
		size += len(msg.ChannelID) + len(msg.ClientMsgNo) + len(msg.FromUID) + len(msg.Payload) + 96
	}
	return size
}

func binaryMaxVarintLen64() int {
	return 10
}
