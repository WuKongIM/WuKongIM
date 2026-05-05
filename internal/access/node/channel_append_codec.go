package node

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

var (
	channelAppendRequestMagic  = [...]byte{'W', 'K', 'C', 'A', 1}
	channelAppendResponseMagic = [...]byte{'W', 'K', 'C', 'R', 1}
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
