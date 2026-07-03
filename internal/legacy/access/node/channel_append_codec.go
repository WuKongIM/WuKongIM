package node

import (
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

var (
	channelAppendRequestMagic       = [...]byte{'W', 'K', 'C', 'A', 1}
	channelAppendResponseMagic      = [...]byte{'W', 'K', 'C', 'R', 1}
	channelAppendBatchRequestMagic  = [...]byte{'W', 'K', 'C', 'B', 1}
	channelAppendBatchResponseMagic = [...]byte{'W', 'K', 'C', 'S', 1}
)

// encodeChannelAppendRequestBinary encodes channel append requests without JSON reflection.
func encodeChannelAppendRequestBinary(req channelAppendRequest) ([]byte, error) {
	dst := make([]byte, 0, len(channelAppendRequestMagic)+128+len(req.AppendRequest.Message.Payload))
	dst = append(dst, channelAppendRequestMagic[:]...)
	dst = appendChannelAppendRequest(dst, req.AppendRequest)
	return dst, nil
}

func decodeChannelAppendRequest(body []byte) (channelAppendRequest, error) {
	if !isChannelAppendRequestBinary(body) {
		return channelAppendRequest{}, fmt.Errorf("access/node: invalid channel append request codec")
	}
	offset := len(channelAppendRequestMagic)
	appendReq, next, err := readChannelAppendRequest(body, offset)
	if err != nil {
		return channelAppendRequest{}, err
	}
	if next != len(body) {
		return channelAppendRequest{}, fmt.Errorf("access/node: trailing channel append request bytes")
	}
	return channelAppendRequest{AppendRequest: appendReq}, nil
}

func encodeChannelAppendResponseBinary(resp channelAppendResponse) ([]byte, error) {
	dst := make([]byte, 0, len(channelAppendResponseMagic)+128+len(resp.Result.Message.Payload))
	dst = append(dst, channelAppendResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendUvarint(dst, resp.LeaderID)
	dst = appendChannelAppendResult(dst, resp.Result)
	return dst, nil
}

func decodeChannelAppendResponseBinary(body []byte) (channelAppendResponse, error) {
	if !isChannelAppendResponseBinary(body) {
		return channelAppendResponse{}, fmt.Errorf("access/node: invalid channel append response codec")
	}
	offset := len(channelAppendResponseMagic)
	var resp channelAppendResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return channelAppendResponse{}, err
	}
	if resp.LeaderID, offset, err = readUvarint(body, offset); err != nil {
		return channelAppendResponse{}, err
	}
	if resp.Result, offset, err = readChannelAppendResult(body, offset); err != nil {
		return channelAppendResponse{}, err
	}
	if offset != len(body) {
		return channelAppendResponse{}, fmt.Errorf("access/node: trailing channel append response bytes")
	}
	return resp, nil
}

func isChannelAppendRequestBinary(body []byte) bool {
	return hasMagic(body, channelAppendRequestMagic[:])
}

func isChannelAppendResponseBinary(body []byte) bool {
	return hasMagic(body, channelAppendResponseMagic[:])
}

// encodeChannelAppendBatchRequestBinary encodes one-channel append batches without JSON reflection.
func encodeChannelAppendBatchRequestBinary(req channelAppendBatchRequest) ([]byte, error) {
	dst := make([]byte, 0, len(channelAppendBatchRequestMagic)+128+estimateChannelMessagesBinarySize(req.AppendBatchRequest.Messages))
	dst = append(dst, channelAppendBatchRequestMagic[:]...)
	dst = appendChannelAppendBatchRequest(dst, req.AppendBatchRequest)
	return dst, nil
}

func decodeChannelAppendBatchRequest(body []byte) (channelAppendBatchRequest, error) {
	if !isChannelAppendBatchRequestBinary(body) {
		return channelAppendBatchRequest{}, fmt.Errorf("access/node: invalid channel append batch request codec")
	}
	offset := len(channelAppendBatchRequestMagic)
	appendReq, next, err := readChannelAppendBatchRequest(body, offset)
	if err != nil {
		return channelAppendBatchRequest{}, err
	}
	if next != len(body) {
		return channelAppendBatchRequest{}, fmt.Errorf("access/node: trailing channel append batch request bytes")
	}
	return channelAppendBatchRequest{AppendBatchRequest: appendReq}, nil
}

func encodeChannelAppendBatchResponseBinary(resp channelAppendBatchResponse) ([]byte, error) {
	dst := make([]byte, 0, len(channelAppendBatchResponseMagic)+128+estimateChannelAppendBatchResultBinarySize(resp.Result))
	dst = append(dst, channelAppendBatchResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendUvarint(dst, resp.LeaderID)
	dst = appendChannelAppendBatchResult(dst, resp.Result)
	return dst, nil
}

func decodeChannelAppendBatchResponseBinary(body []byte) (channelAppendBatchResponse, error) {
	if !isChannelAppendBatchResponseBinary(body) {
		return channelAppendBatchResponse{}, fmt.Errorf("access/node: invalid channel append batch response codec")
	}
	offset := len(channelAppendBatchResponseMagic)
	var resp channelAppendBatchResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return channelAppendBatchResponse{}, err
	}
	if resp.LeaderID, offset, err = readUvarint(body, offset); err != nil {
		return channelAppendBatchResponse{}, err
	}
	if resp.Result, offset, err = readChannelAppendBatchResult(body, offset); err != nil {
		return channelAppendBatchResponse{}, err
	}
	if offset != len(body) {
		return channelAppendBatchResponse{}, fmt.Errorf("access/node: trailing channel append batch response bytes")
	}
	return resp, nil
}

func isChannelAppendBatchRequestBinary(body []byte) bool {
	return hasMagic(body, channelAppendBatchRequestMagic[:])
}

func isChannelAppendBatchResponseBinary(body []byte) bool {
	return hasMagic(body, channelAppendBatchResponseMagic[:])
}

func appendChannelAppendRequest(dst []byte, req channel.AppendRequest) []byte {
	dst = appendChannelID(dst, req.ChannelID)
	dst = appendChannelMessage(dst, req.Message)
	dst = appendNodeBool(dst, req.SupportsMessageSeqU64)
	dst = append(dst, byte(req.CommitMode))
	dst = appendUvarint(dst, req.ExpectedChannelEpoch)
	dst = appendUvarint(dst, req.ExpectedLeaderEpoch)
	return dst
}

func readChannelAppendRequest(body []byte, offset int) (channel.AppendRequest, int, error) {
	var req channel.AppendRequest
	var err error
	if req.ChannelID, offset, err = readChannelID(body, offset); err != nil {
		return channel.AppendRequest{}, offset, err
	}
	if req.Message, offset, err = readChannelMessage(body, offset); err != nil {
		return channel.AppendRequest{}, offset, err
	}
	if req.SupportsMessageSeqU64, offset, err = readNodeBool(body, offset); err != nil {
		return channel.AppendRequest{}, offset, err
	}
	var commitMode byte
	if commitMode, offset, err = readByte(body, offset, "channel append commit mode"); err != nil {
		return channel.AppendRequest{}, offset, err
	}
	req.CommitMode = channel.CommitMode(commitMode)
	if req.ExpectedChannelEpoch, offset, err = readUvarint(body, offset); err != nil {
		return channel.AppendRequest{}, offset, err
	}
	if req.ExpectedLeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return channel.AppendRequest{}, offset, err
	}
	return req, offset, nil
}

func appendChannelAppendBatchRequest(dst []byte, req channel.AppendBatchRequest) []byte {
	dst = appendChannelID(dst, req.ChannelID)
	dst = appendChannelMessageSlice(dst, req.Messages)
	dst = appendNodeBool(dst, req.SupportsMessageSeqU64)
	dst = append(dst, byte(req.CommitMode))
	dst = appendUvarint(dst, req.ExpectedChannelEpoch)
	dst = appendUvarint(dst, req.ExpectedLeaderEpoch)
	return dst
}

func readChannelAppendBatchRequest(body []byte, offset int) (channel.AppendBatchRequest, int, error) {
	var req channel.AppendBatchRequest
	var err error
	if req.ChannelID, offset, err = readChannelID(body, offset); err != nil {
		return channel.AppendBatchRequest{}, offset, err
	}
	if req.Messages, offset, err = readChannelMessageSlice(body, offset); err != nil {
		return channel.AppendBatchRequest{}, offset, err
	}
	if req.SupportsMessageSeqU64, offset, err = readNodeBool(body, offset); err != nil {
		return channel.AppendBatchRequest{}, offset, err
	}
	var commitMode byte
	if commitMode, offset, err = readByte(body, offset, "channel append batch commit mode"); err != nil {
		return channel.AppendBatchRequest{}, offset, err
	}
	req.CommitMode = channel.CommitMode(commitMode)
	if req.ExpectedChannelEpoch, offset, err = readUvarint(body, offset); err != nil {
		return channel.AppendBatchRequest{}, offset, err
	}
	if req.ExpectedLeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return channel.AppendBatchRequest{}, offset, err
	}
	return req, offset, nil
}

func appendChannelAppendResult(dst []byte, result channel.AppendResult) []byte {
	dst = appendUvarint(dst, result.MessageID)
	dst = appendUvarint(dst, result.MessageSeq)
	dst = appendChannelMessage(dst, result.Message)
	return dst
}

func readChannelAppendResult(body []byte, offset int) (channel.AppendResult, int, error) {
	var result channel.AppendResult
	var err error
	if result.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return channel.AppendResult{}, offset, err
	}
	if result.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return channel.AppendResult{}, offset, err
	}
	if result.Message, offset, err = readChannelMessage(body, offset); err != nil {
		return channel.AppendResult{}, offset, err
	}
	return result, offset, nil
}

func appendChannelAppendBatchResult(dst []byte, result channel.AppendBatchResult) []byte {
	dst = appendUvarint(dst, uint64(len(result.Items)))
	for _, item := range result.Items {
		dst = appendString(dst, channelAppendItemErrorCode(item.Err))
		dst = appendUvarint(dst, item.MessageID)
		dst = appendUvarint(dst, item.MessageSeq)
		dst = appendChannelMessage(dst, item.Message)
	}
	return dst
}

func readChannelAppendBatchResult(body []byte, offset int) (channel.AppendBatchResult, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return channel.AppendBatchResult{}, offset, err
	}
	offset = next
	itemsLen, err := readCollectionLen(count, len(body)-offset, "channel append batch result")
	if err != nil {
		return channel.AppendBatchResult{}, offset, err
	}
	items := make([]channel.AppendBatchItemResult, itemsLen)
	for i := range items {
		var errCode string
		if errCode, offset, err = readString(body, offset); err != nil {
			return channel.AppendBatchResult{}, offset, err
		}
		items[i].Err = channelAppendItemErrorFromCode(errCode)
		if items[i].MessageID, offset, err = readUvarint(body, offset); err != nil {
			return channel.AppendBatchResult{}, offset, err
		}
		if items[i].MessageSeq, offset, err = readUvarint(body, offset); err != nil {
			return channel.AppendBatchResult{}, offset, err
		}
		if items[i].Message, offset, err = readChannelMessage(body, offset); err != nil {
			return channel.AppendBatchResult{}, offset, err
		}
	}
	return channel.AppendBatchResult{Items: items}, offset, nil
}

func estimateChannelAppendBatchResultBinarySize(result channel.AppendBatchResult) int {
	messages := make([]channel.Message, 0, len(result.Items))
	size := binaryMaxVarintLen64()
	for _, item := range result.Items {
		size += len(channelAppendItemErrorCode(item.Err)) + binaryMaxVarintLen64()*3
		messages = append(messages, item.Message)
	}
	return size + estimateChannelMessagesBinarySize(messages)
}

func channelAppendItemErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, channel.ErrInvalidArgument):
		return channel.ErrInvalidArgument.Error()
	case errors.Is(err, channel.ErrStaleMeta):
		return channel.ErrStaleMeta.Error()
	case errors.Is(err, channel.ErrNotLeader):
		return channel.ErrNotLeader.Error()
	case errors.Is(err, channel.ErrNotReady):
		return channel.ErrNotReady.Error()
	case errors.Is(err, channel.ErrLeaseExpired):
		return channel.ErrLeaseExpired.Error()
	case errors.Is(err, channel.ErrWriteFenced):
		return channel.ErrWriteFenced.Error()
	case errors.Is(err, channel.ErrChannelDeleting):
		return channel.ErrChannelDeleting.Error()
	case errors.Is(err, channel.ErrChannelNotFound):
		return channel.ErrChannelNotFound.Error()
	case errors.Is(err, channel.ErrIdempotencyConflict):
		return channel.ErrIdempotencyConflict.Error()
	case errors.Is(err, channel.ErrProtocolUpgradeRequired):
		return channel.ErrProtocolUpgradeRequired.Error()
	case errors.Is(err, channel.ErrMessageSeqExhausted):
		return channel.ErrMessageSeqExhausted.Error()
	default:
		return err.Error()
	}
}

func channelAppendItemErrorFromCode(code string) error {
	switch code {
	case "":
		return nil
	case channel.ErrInvalidArgument.Error():
		return channel.ErrInvalidArgument
	case channel.ErrStaleMeta.Error():
		return channel.ErrStaleMeta
	case channel.ErrNotLeader.Error():
		return channel.ErrNotLeader
	case channel.ErrNotReady.Error():
		return channel.ErrNotReady
	case channel.ErrLeaseExpired.Error():
		return channel.ErrLeaseExpired
	case channel.ErrWriteFenced.Error():
		return channel.ErrWriteFenced
	case channel.ErrChannelDeleting.Error():
		return channel.ErrChannelDeleting
	case channel.ErrChannelNotFound.Error():
		return channel.ErrChannelNotFound
	case channel.ErrIdempotencyConflict.Error():
		return channel.ErrIdempotencyConflict
	case channel.ErrProtocolUpgradeRequired.Error():
		return channel.ErrProtocolUpgradeRequired
	case channel.ErrMessageSeqExhausted.Error():
		return channel.ErrMessageSeqExhausted
	default:
		return fmt.Errorf("access/node: remote channel append item error: %s", code)
	}
}
