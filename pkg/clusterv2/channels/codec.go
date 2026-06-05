package channels

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

const codecVersion = uint8(3)

const (
	kindPull uint8 = iota + 1
	kindPullResponse
	kindAck
	kindPullHint
	kindNotify
	kindAppend
	kindAppendResponse
	kindAppendBatch
	kindAppendBatchResponse
)

// EncodePullRequest encodes a ChannelV2 pull request.
func EncodePullRequest(req channeltransport.PullRequest) ([]byte, error) {
	return encodeFrame(kindPull, appendPullRequest(nil, req)), nil
}

// DecodePullRequest decodes a ChannelV2 pull request.
func DecodePullRequest(data []byte) (channeltransport.PullRequest, error) {
	payload, err := decodeFrame(data, kindPull)
	if err != nil {
		return channeltransport.PullRequest{}, err
	}
	req, offset, err := readPullRequest(payload, 0)
	if err != nil {
		return channeltransport.PullRequest{}, err
	}
	if offset != len(payload) {
		return channeltransport.PullRequest{}, fmt.Errorf("channels: trailing pull request bytes")
	}
	return req, nil
}

func encodePullResponse(resp channeltransport.PullResponse) ([]byte, error) {
	return encodeRPCResult(kindPullResponse, resp, nil)
}
func decodePullResponse(data []byte) (channeltransport.PullResponse, error) {
	var resp channeltransport.PullResponse
	return resp, decodeRPCResult(data, kindPullResponse, &resp)
}
func encodeAckRequest(req channeltransport.AckRequest) ([]byte, error) {
	return encodeFrame(kindAck, appendAckRequest(nil, req)), nil
}
func decodeAckRequest(data []byte) (channeltransport.AckRequest, error) {
	payload, err := decodeFrame(data, kindAck)
	if err != nil {
		return channeltransport.AckRequest{}, err
	}
	req, offset, err := readAckRequest(payload, 0)
	if err != nil {
		return channeltransport.AckRequest{}, err
	}
	if offset != len(payload) {
		return channeltransport.AckRequest{}, fmt.Errorf("channels: trailing ack request bytes")
	}
	return req, nil
}
func encodePullHintRequest(req channeltransport.PullHintRequest) ([]byte, error) {
	return encodeFrame(kindPullHint, appendPullHintRequest(nil, req)), nil
}
func decodePullHintRequest(data []byte) (channeltransport.PullHintRequest, error) {
	payload, err := decodeFrame(data, kindPullHint)
	if err != nil {
		return channeltransport.PullHintRequest{}, err
	}
	req, offset, err := readPullHintRequest(payload, 0)
	if err != nil {
		return channeltransport.PullHintRequest{}, err
	}
	if offset != len(payload) {
		return channeltransport.PullHintRequest{}, fmt.Errorf("channels: trailing pull hint request bytes")
	}
	return req, nil
}
func encodeNotifyRequest(req channeltransport.NotifyRequest) ([]byte, error) {
	return encodeFrame(kindNotify, appendNotifyRequest(nil, req)), nil
}
func decodeNotifyRequest(data []byte) (channeltransport.NotifyRequest, error) {
	payload, err := decodeFrame(data, kindNotify)
	if err != nil {
		return channeltransport.NotifyRequest{}, err
	}
	req, offset, err := readNotifyRequest(payload, 0)
	if err != nil {
		return channeltransport.NotifyRequest{}, err
	}
	if offset != len(payload) {
		return channeltransport.NotifyRequest{}, fmt.Errorf("channels: trailing notify request bytes")
	}
	return req, nil
}
func encodeAppendRequest(req ch.AppendRequest) ([]byte, error) {
	return encodeFrame(kindAppend, appendAppendRequest(nil, req)), nil
}
func decodeAppendRequest(data []byte) (ch.AppendRequest, error) {
	payload, err := decodeFrame(data, kindAppend)
	if err != nil {
		return ch.AppendRequest{}, err
	}
	req, offset, err := readAppendRequest(payload, 0)
	if err != nil {
		return ch.AppendRequest{}, err
	}
	if offset != len(payload) {
		return ch.AppendRequest{}, fmt.Errorf("channels: trailing append request bytes")
	}
	return req, nil
}
func encodeAppendResponse(resp ch.AppendResult) ([]byte, error) {
	return encodeRPCResult(kindAppendResponse, resp, nil)
}
func decodeAppendResponse(data []byte) (ch.AppendResult, error) {
	var resp ch.AppendResult
	return resp, decodeRPCResult(data, kindAppendResponse, &resp)
}
func encodeAppendBatchRequest(req ch.AppendBatchRequest) ([]byte, error) {
	return encodeFrame(kindAppendBatch, appendAppendBatchRequest(nil, req)), nil
}
func decodeAppendBatchRequest(data []byte) (ch.AppendBatchRequest, error) {
	payload, err := decodeFrame(data, kindAppendBatch)
	if err != nil {
		return ch.AppendBatchRequest{}, err
	}
	req, offset, err := readAppendBatchRequest(payload, 0)
	if err != nil {
		return ch.AppendBatchRequest{}, err
	}
	if offset != len(payload) {
		return ch.AppendBatchRequest{}, fmt.Errorf("channels: trailing append batch request bytes")
	}
	return req, nil
}
func encodeAppendBatchResponse(resp ch.AppendBatchResult) ([]byte, error) {
	return encodeRPCResult(kindAppendBatchResponse, resp, nil)
}
func decodeAppendBatchResponse(data []byte) (ch.AppendBatchResult, error) {
	var resp ch.AppendBatchResult
	return resp, decodeRPCResult(data, kindAppendBatchResponse, &resp)
}

// rpcApplicationError is the compact cross-node form for sentinel errors.
type rpcApplicationError struct {
	Code    string
	Message string
}

const (
	rpcErrorUnknown         = "unknown"
	rpcErrorInvalidConfig   = "invalid_config"
	rpcErrorBackpressured   = "backpressured"
	rpcErrorNotLeader       = "not_leader"
	rpcErrorNotReady        = "not_ready"
	rpcErrorStaleMeta       = "stale_meta"
	rpcErrorChannelNotFound = "channel_not_found"
	rpcErrorNotReplica      = "not_replica"
	rpcErrorClosed          = "closed"
	rpcErrorTooManyChannels = "too_many_channels"
)

const (
	rpcResultOK uint8 = iota
	rpcResultErr
)

func encodeRPCResult(kind uint8, payload any, err error) ([]byte, error) {
	if err != nil {
		dst := []byte{rpcResultErr}
		dst = appendRPCApplicationError(dst, rpcApplicationError{Code: rpcErrorCode(err), Message: err.Error()})
		return encodeFrame(kind, dst), nil
	}
	dst := []byte{rpcResultOK}
	if payload != nil {
		var ok bool
		dst, ok = appendRPCPayload(dst, payload)
		if !ok {
			return nil, fmt.Errorf("channels: unsupported rpc result payload %T", payload)
		}
	}
	return encodeFrame(kind, dst), nil
}

func decodeRPCResult(data []byte, kind uint8, payload any) error {
	body, err := decodeFrame(data, kind)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("channels: missing result status")
	}
	status := body[0]
	offset := 1
	if status == rpcResultErr {
		appErr, next, err := readRPCApplicationError(body, offset)
		if err != nil {
			return err
		}
		if next != len(body) {
			return fmt.Errorf("channels: trailing result error bytes")
		}
		return decodeRPCApplicationError(appErr)
	}
	if status != rpcResultOK {
		return fmt.Errorf("channels: invalid result status")
	}
	if payload == nil {
		if offset != len(body) {
			return fmt.Errorf("channels: trailing empty result bytes")
		}
		return nil
	}
	offset, err = readRPCPayload(body, offset, payload)
	if err != nil {
		return err
	}
	if offset != len(body) {
		return fmt.Errorf("channels: trailing result payload bytes")
	}
	return nil
}

func encodeFrame(kind uint8, payload []byte) []byte {
	out := make([]byte, 0, 2+len(payload))
	out = append(out, codecVersion, kind)
	return append(out, payload...)
}

func decodeFrame(data []byte, wantKind uint8) ([]byte, error) {
	if len(data) < 2 || data[0] != codecVersion || data[1] != wantKind {
		return nil, fmt.Errorf("channels: invalid frame")
	}
	return data[2:], nil
}

func appendRPCPayload(dst []byte, payload any) ([]byte, bool) {
	switch v := payload.(type) {
	case channeltransport.PullResponse:
		return appendPullResponse(dst, v), true
	case ch.AppendResult:
		return appendAppendResult(dst, v), true
	case ch.AppendBatchResult:
		return appendAppendBatchResult(dst, v), true
	default:
		return dst, false
	}
}

func readRPCPayload(body []byte, offset int, payload any) (int, error) {
	var err error
	switch v := payload.(type) {
	case *channeltransport.PullResponse:
		*v, offset, err = readPullResponse(body, offset)
	case *ch.AppendResult:
		*v, offset, err = readAppendResult(body, offset)
	case *ch.AppendBatchResult:
		*v, offset, err = readAppendBatchResult(body, offset)
	default:
		return offset, fmt.Errorf("channels: unsupported rpc result target %T", payload)
	}
	return offset, err
}

func appendPullRequest(dst []byte, req channeltransport.PullRequest) []byte {
	dst = appendChannelKey(dst, req.ChannelKey)
	dst = appendChannelID(dst, req.ChannelID)
	dst = appendUvarint(dst, req.Epoch)
	dst = appendUvarint(dst, req.LeaderEpoch)
	dst = appendUvarint(dst, uint64(req.Follower))
	dst = appendUvarint(dst, req.NextOffset)
	dst = appendUvarint(dst, req.AckOffset)
	dst = appendVarint(dst, int64(req.MaxBytes))
	dst = appendBool(dst, req.NeedMeta)
	return dst
}

func readPullRequest(body []byte, offset int) (channeltransport.PullRequest, int, error) {
	var req channeltransport.PullRequest
	var err error
	if req.ChannelKey, offset, err = readChannelKey(body, offset); err != nil {
		return channeltransport.PullRequest{}, offset, err
	}
	if req.ChannelID, offset, err = readChannelID(body, offset); err != nil {
		return channeltransport.PullRequest{}, offset, err
	}
	if req.Epoch, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullRequest{}, offset, err
	}
	if req.LeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullRequest{}, offset, err
	}
	var follower uint64
	if follower, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullRequest{}, offset, err
	}
	req.Follower = ch.NodeID(follower)
	if req.NextOffset, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullRequest{}, offset, err
	}
	if req.AckOffset, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullRequest{}, offset, err
	}
	if req.MaxBytes, offset, err = readInt(body, offset, "pull max bytes"); err != nil {
		return channeltransport.PullRequest{}, offset, err
	}
	if req.NeedMeta, offset, err = readBool(body, offset, "pull need meta"); err != nil {
		return channeltransport.PullRequest{}, offset, err
	}
	return req, offset, nil
}

func appendPullResponse(dst []byte, resp channeltransport.PullResponse) []byte {
	dst = appendChannelKey(dst, resp.ChannelKey)
	dst = appendUvarint(dst, resp.Epoch)
	dst = appendUvarint(dst, resp.LeaderEpoch)
	dst = appendUvarint(dst, resp.LeaderHW)
	dst = appendUvarint(dst, resp.LeaderLEO)
	dst = appendUvarint(dst, resp.ActivityVersion)
	dst = appendVarint(dst, int64(resp.NextPullAfter))
	dst = append(dst, byte(resp.Control))
	dst = appendMetaPtr(dst, resp.Meta)
	dst = appendRecords(dst, resp.Records)
	return dst
}

func readPullResponse(body []byte, offset int) (channeltransport.PullResponse, int, error) {
	var resp channeltransport.PullResponse
	var err error
	if resp.ChannelKey, offset, err = readChannelKey(body, offset); err != nil {
		return channeltransport.PullResponse{}, offset, err
	}
	if resp.Epoch, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullResponse{}, offset, err
	}
	if resp.LeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullResponse{}, offset, err
	}
	if resp.LeaderHW, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullResponse{}, offset, err
	}
	if resp.LeaderLEO, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullResponse{}, offset, err
	}
	if resp.ActivityVersion, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullResponse{}, offset, err
	}
	var nextPullAfter int64
	if nextPullAfter, offset, err = readVarint(body, offset); err != nil {
		return channeltransport.PullResponse{}, offset, err
	}
	resp.NextPullAfter = time.Duration(nextPullAfter)
	var control byte
	if control, offset, err = readByte(body, offset, "pull response control"); err != nil {
		return channeltransport.PullResponse{}, offset, err
	}
	resp.Control = channeltransport.PullControl(control)
	if resp.Meta, offset, err = readMetaPtr(body, offset); err != nil {
		return channeltransport.PullResponse{}, offset, err
	}
	if resp.Records, offset, err = readRecords(body, offset); err != nil {
		return channeltransport.PullResponse{}, offset, err
	}
	return resp, offset, nil
}

func appendAckRequest(dst []byte, req channeltransport.AckRequest) []byte {
	dst = appendChannelKey(dst, req.ChannelKey)
	dst = appendUvarint(dst, req.Epoch)
	dst = appendUvarint(dst, req.LeaderEpoch)
	dst = appendUvarint(dst, uint64(req.Follower))
	dst = appendUvarint(dst, req.MatchOffset)
	dst = appendUvarint(dst, req.ActivityVersion)
	dst = appendBool(dst, req.Stopped)
	return dst
}

func readAckRequest(body []byte, offset int) (channeltransport.AckRequest, int, error) {
	var req channeltransport.AckRequest
	var err error
	if req.ChannelKey, offset, err = readChannelKey(body, offset); err != nil {
		return channeltransport.AckRequest{}, offset, err
	}
	if req.Epoch, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.AckRequest{}, offset, err
	}
	if req.LeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.AckRequest{}, offset, err
	}
	var follower uint64
	if follower, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.AckRequest{}, offset, err
	}
	req.Follower = ch.NodeID(follower)
	if req.MatchOffset, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.AckRequest{}, offset, err
	}
	if req.ActivityVersion, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.AckRequest{}, offset, err
	}
	if req.Stopped, offset, err = readBool(body, offset, "ack stopped"); err != nil {
		return channeltransport.AckRequest{}, offset, err
	}
	return req, offset, nil
}

func appendPullHintRequest(dst []byte, req channeltransport.PullHintRequest) []byte {
	dst = appendChannelKey(dst, req.ChannelKey)
	dst = appendChannelID(dst, req.ChannelID)
	dst = appendUvarint(dst, req.Epoch)
	dst = appendUvarint(dst, req.LeaderEpoch)
	dst = appendUvarint(dst, uint64(req.Leader))
	dst = appendUvarint(dst, req.LeaderLEO)
	dst = appendUvarint(dst, req.ActivityVersion)
	dst = append(dst, byte(req.Reason))
	return dst
}

func readPullHintRequest(body []byte, offset int) (channeltransport.PullHintRequest, int, error) {
	var req channeltransport.PullHintRequest
	var err error
	if req.ChannelKey, offset, err = readChannelKey(body, offset); err != nil {
		return channeltransport.PullHintRequest{}, offset, err
	}
	if req.ChannelID, offset, err = readChannelID(body, offset); err != nil {
		return channeltransport.PullHintRequest{}, offset, err
	}
	if req.Epoch, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullHintRequest{}, offset, err
	}
	if req.LeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullHintRequest{}, offset, err
	}
	var leader uint64
	if leader, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullHintRequest{}, offset, err
	}
	req.Leader = ch.NodeID(leader)
	if req.LeaderLEO, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullHintRequest{}, offset, err
	}
	if req.ActivityVersion, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.PullHintRequest{}, offset, err
	}
	var reason byte
	if reason, offset, err = readByte(body, offset, "pull hint reason"); err != nil {
		return channeltransport.PullHintRequest{}, offset, err
	}
	req.Reason = channeltransport.PullHintReason(reason)
	return req, offset, nil
}

func appendNotifyRequest(dst []byte, req channeltransport.NotifyRequest) []byte {
	dst = appendChannelKey(dst, req.ChannelKey)
	dst = appendChannelID(dst, req.ChannelID)
	dst = appendUvarint(dst, req.Epoch)
	dst = appendUvarint(dst, req.LeaderEpoch)
	dst = appendUvarint(dst, uint64(req.Leader))
	dst = appendUvarint(dst, req.LeaderLEO)
	return dst
}

func readNotifyRequest(body []byte, offset int) (channeltransport.NotifyRequest, int, error) {
	var req channeltransport.NotifyRequest
	var err error
	if req.ChannelKey, offset, err = readChannelKey(body, offset); err != nil {
		return channeltransport.NotifyRequest{}, offset, err
	}
	if req.ChannelID, offset, err = readChannelID(body, offset); err != nil {
		return channeltransport.NotifyRequest{}, offset, err
	}
	if req.Epoch, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.NotifyRequest{}, offset, err
	}
	if req.LeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.NotifyRequest{}, offset, err
	}
	var leader uint64
	if leader, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.NotifyRequest{}, offset, err
	}
	req.Leader = ch.NodeID(leader)
	if req.LeaderLEO, offset, err = readUvarint(body, offset); err != nil {
		return channeltransport.NotifyRequest{}, offset, err
	}
	return req, offset, nil
}

func appendAppendRequest(dst []byte, req ch.AppendRequest) []byte {
	dst = appendChannelID(dst, req.ChannelID)
	dst = appendMessage(dst, req.Message)
	dst = append(dst, byte(req.CommitMode))
	dst = appendUvarint(dst, req.ExpectedChannelEpoch)
	dst = appendUvarint(dst, req.ExpectedLeaderEpoch)
	return dst
}

func readAppendRequest(body []byte, offset int) (ch.AppendRequest, int, error) {
	var req ch.AppendRequest
	var err error
	if req.ChannelID, offset, err = readChannelID(body, offset); err != nil {
		return ch.AppendRequest{}, offset, err
	}
	if req.Message, offset, err = readMessage(body, offset); err != nil {
		return ch.AppendRequest{}, offset, err
	}
	var commitMode byte
	if commitMode, offset, err = readByte(body, offset, "append commit mode"); err != nil {
		return ch.AppendRequest{}, offset, err
	}
	req.CommitMode = ch.CommitMode(commitMode)
	if req.ExpectedChannelEpoch, offset, err = readUvarint(body, offset); err != nil {
		return ch.AppendRequest{}, offset, err
	}
	if req.ExpectedLeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return ch.AppendRequest{}, offset, err
	}
	return req, offset, nil
}

func appendAppendResult(dst []byte, result ch.AppendResult) []byte {
	dst = appendUvarint(dst, result.MessageID)
	dst = appendUvarint(dst, result.MessageSeq)
	dst = appendMessage(dst, result.Message)
	return dst
}

func readAppendResult(body []byte, offset int) (ch.AppendResult, int, error) {
	var result ch.AppendResult
	var err error
	if result.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return ch.AppendResult{}, offset, err
	}
	if result.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return ch.AppendResult{}, offset, err
	}
	if result.Message, offset, err = readMessage(body, offset); err != nil {
		return ch.AppendResult{}, offset, err
	}
	return result, offset, nil
}

func appendAppendBatchRequest(dst []byte, req ch.AppendBatchRequest) []byte {
	dst = appendChannelID(dst, req.ChannelID)
	dst = appendMessages(dst, req.Messages)
	dst = appendString(dst, req.TraceID)
	dst = appendChannelKey(dst, ch.ChannelKey(req.ChannelKey))
	dst = appendVarint(dst, int64(req.Attempt))
	dst = append(dst, byte(req.CommitMode))
	dst = appendUvarint(dst, req.ExpectedChannelEpoch)
	dst = appendUvarint(dst, req.ExpectedLeaderEpoch)
	dst = appendBool(dst, req.OmitResultPayload)
	return dst
}

func readAppendBatchRequest(body []byte, offset int) (ch.AppendBatchRequest, int, error) {
	var req ch.AppendBatchRequest
	var err error
	if req.ChannelID, offset, err = readChannelID(body, offset); err != nil {
		return ch.AppendBatchRequest{}, offset, err
	}
	if req.Messages, offset, err = readMessages(body, offset); err != nil {
		return ch.AppendBatchRequest{}, offset, err
	}
	if req.TraceID, offset, err = readString(body, offset); err != nil {
		return ch.AppendBatchRequest{}, offset, err
	}
	var channelKey ch.ChannelKey
	if channelKey, offset, err = readChannelKey(body, offset); err != nil {
		return ch.AppendBatchRequest{}, offset, err
	}
	req.ChannelKey = string(channelKey)
	if req.Attempt, offset, err = readInt(body, offset, "append batch attempt"); err != nil {
		return ch.AppendBatchRequest{}, offset, err
	}
	var commitMode byte
	if commitMode, offset, err = readByte(body, offset, "append batch commit mode"); err != nil {
		return ch.AppendBatchRequest{}, offset, err
	}
	req.CommitMode = ch.CommitMode(commitMode)
	if req.ExpectedChannelEpoch, offset, err = readUvarint(body, offset); err != nil {
		return ch.AppendBatchRequest{}, offset, err
	}
	if req.ExpectedLeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return ch.AppendBatchRequest{}, offset, err
	}
	if req.OmitResultPayload, offset, err = readBool(body, offset, "append batch omit result payload"); err != nil {
		return ch.AppendBatchRequest{}, offset, err
	}
	return req, offset, nil
}

func appendAppendBatchResult(dst []byte, result ch.AppendBatchResult) []byte {
	dst = appendSliceHeader(dst, len(result.Items), result.Items == nil)
	for _, item := range result.Items {
		dst = appendOptionalRPCApplicationError(dst, item.Err)
		dst = appendUvarint(dst, item.MessageID)
		dst = appendUvarint(dst, item.MessageSeq)
		dst = appendMessage(dst, item.Message)
	}
	return dst
}

func readAppendBatchResult(body []byte, offset int) (ch.AppendBatchResult, int, error) {
	nilSlice, count, next, err := readSliceHeader(body, offset, "append batch result")
	if err != nil {
		return ch.AppendBatchResult{}, offset, err
	}
	offset = next
	if nilSlice {
		return ch.AppendBatchResult{}, offset, nil
	}
	items := make([]ch.AppendBatchItemResult, count)
	for i := range items {
		if items[i].Err, offset, err = readOptionalRPCApplicationError(body, offset); err != nil {
			return ch.AppendBatchResult{}, offset, err
		}
		if items[i].MessageID, offset, err = readUvarint(body, offset); err != nil {
			return ch.AppendBatchResult{}, offset, err
		}
		if items[i].MessageSeq, offset, err = readUvarint(body, offset); err != nil {
			return ch.AppendBatchResult{}, offset, err
		}
		if items[i].Message, offset, err = readMessage(body, offset); err != nil {
			return ch.AppendBatchResult{}, offset, err
		}
	}
	return ch.AppendBatchResult{Items: items}, offset, nil
}

func appendChannelKey(dst []byte, key ch.ChannelKey) []byte {
	return appendString(dst, string(key))
}

func readChannelKey(body []byte, offset int) (ch.ChannelKey, int, error) {
	value, next, err := readString(body, offset)
	return ch.ChannelKey(value), next, err
}

func appendChannelID(dst []byte, id ch.ChannelID) []byte {
	dst = appendString(dst, id.ID)
	return append(dst, id.Type)
}

func readChannelID(body []byte, offset int) (ch.ChannelID, int, error) {
	id, next, err := readString(body, offset)
	if err != nil {
		return ch.ChannelID{}, offset, err
	}
	offset = next
	channelType, next, err := readByte(body, offset, "channel type")
	if err != nil {
		return ch.ChannelID{}, offset, err
	}
	return ch.ChannelID{ID: id, Type: channelType}, next, nil
}

func appendMessage(dst []byte, msg ch.Message) []byte {
	dst = appendUvarint(dst, msg.MessageID)
	dst = appendUvarint(dst, msg.MessageSeq)
	dst = appendString(dst, msg.ChannelID)
	dst = append(dst, msg.ChannelType)
	dst = appendString(dst, msg.FromUID)
	dst = appendString(dst, msg.ClientMsgNo)
	dst = appendString(dst, msg.TraceID)
	dst = appendChannelKey(dst, ch.ChannelKey(msg.ChannelKey))
	dst = appendOptionalBytes(dst, msg.Payload)
	return dst
}

func readMessage(body []byte, offset int) (ch.Message, int, error) {
	var msg ch.Message
	var err error
	if msg.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return ch.Message{}, offset, err
	}
	if msg.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return ch.Message{}, offset, err
	}
	if msg.ChannelID, offset, err = readString(body, offset); err != nil {
		return ch.Message{}, offset, err
	}
	if msg.ChannelType, offset, err = readByte(body, offset, "message channel type"); err != nil {
		return ch.Message{}, offset, err
	}
	if msg.FromUID, offset, err = readString(body, offset); err != nil {
		return ch.Message{}, offset, err
	}
	if msg.ClientMsgNo, offset, err = readString(body, offset); err != nil {
		return ch.Message{}, offset, err
	}
	if msg.TraceID, offset, err = readString(body, offset); err != nil {
		return ch.Message{}, offset, err
	}
	var channelKey ch.ChannelKey
	if channelKey, offset, err = readChannelKey(body, offset); err != nil {
		return ch.Message{}, offset, err
	}
	msg.ChannelKey = string(channelKey)
	if msg.Payload, offset, err = readOptionalBytes(body, offset, "message payload"); err != nil {
		return ch.Message{}, offset, err
	}
	return msg, offset, nil
}

func appendMessages(dst []byte, messages []ch.Message) []byte {
	dst = appendSliceHeader(dst, len(messages), messages == nil)
	for _, msg := range messages {
		dst = appendMessage(dst, msg)
	}
	return dst
}

func readMessages(body []byte, offset int) ([]ch.Message, int, error) {
	nilSlice, count, next, err := readSliceHeader(body, offset, "messages")
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if nilSlice {
		return nil, offset, nil
	}
	messages := make([]ch.Message, count)
	for i := range messages {
		if messages[i], offset, err = readMessage(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return messages, offset, nil
}

func appendMetaPtr(dst []byte, meta *ch.Meta) []byte {
	if meta == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendMeta(dst, *meta)
}

func readMetaPtr(body []byte, offset int) (*ch.Meta, int, error) {
	present, next, err := readByte(body, offset, "meta presence")
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if present == 0 {
		return nil, offset, nil
	}
	if present != 1 {
		return nil, offset, fmt.Errorf("channels: invalid meta presence")
	}
	meta, next, err := readMeta(body, offset)
	if err != nil {
		return nil, offset, err
	}
	return &meta, next, nil
}

func appendMeta(dst []byte, meta ch.Meta) []byte {
	dst = appendChannelKey(dst, meta.Key)
	dst = appendChannelID(dst, meta.ID)
	dst = appendUvarint(dst, meta.Epoch)
	dst = appendUvarint(dst, meta.LeaderEpoch)
	dst = appendUvarint(dst, uint64(meta.Leader))
	dst = appendNodeIDs(dst, meta.Replicas)
	dst = appendNodeIDs(dst, meta.ISR)
	dst = appendVarint(dst, int64(meta.MinISR))
	dst = appendTime(dst, meta.LeaseUntil)
	dst = append(dst, byte(meta.Status))
	return dst
}

func readMeta(body []byte, offset int) (ch.Meta, int, error) {
	var meta ch.Meta
	var err error
	if meta.Key, offset, err = readChannelKey(body, offset); err != nil {
		return ch.Meta{}, offset, err
	}
	if meta.ID, offset, err = readChannelID(body, offset); err != nil {
		return ch.Meta{}, offset, err
	}
	if meta.Epoch, offset, err = readUvarint(body, offset); err != nil {
		return ch.Meta{}, offset, err
	}
	if meta.LeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return ch.Meta{}, offset, err
	}
	var leader uint64
	if leader, offset, err = readUvarint(body, offset); err != nil {
		return ch.Meta{}, offset, err
	}
	meta.Leader = ch.NodeID(leader)
	if meta.Replicas, offset, err = readNodeIDs(body, offset); err != nil {
		return ch.Meta{}, offset, err
	}
	if meta.ISR, offset, err = readNodeIDs(body, offset); err != nil {
		return ch.Meta{}, offset, err
	}
	if meta.MinISR, offset, err = readInt(body, offset, "meta min isr"); err != nil {
		return ch.Meta{}, offset, err
	}
	if meta.LeaseUntil, offset, err = readTime(body, offset); err != nil {
		return ch.Meta{}, offset, err
	}
	var status byte
	if status, offset, err = readByte(body, offset, "meta status"); err != nil {
		return ch.Meta{}, offset, err
	}
	meta.Status = ch.Status(status)
	return meta, offset, nil
}

func appendNodeIDs(dst []byte, ids []ch.NodeID) []byte {
	dst = appendSliceHeader(dst, len(ids), ids == nil)
	for _, id := range ids {
		dst = appendUvarint(dst, uint64(id))
	}
	return dst
}

func readNodeIDs(body []byte, offset int) ([]ch.NodeID, int, error) {
	nilSlice, count, next, err := readSliceHeader(body, offset, "node ids")
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if nilSlice {
		return nil, offset, nil
	}
	ids := make([]ch.NodeID, count)
	for i := range ids {
		var value uint64
		if value, offset, err = readUvarint(body, offset); err != nil {
			return nil, offset, err
		}
		ids[i] = ch.NodeID(value)
	}
	return ids, offset, nil
}

func appendRecords(dst []byte, records []ch.Record) []byte {
	dst = appendSliceHeader(dst, len(records), records == nil)
	for _, record := range records {
		dst = appendRecord(dst, record)
	}
	return dst
}

func readRecords(body []byte, offset int) ([]ch.Record, int, error) {
	nilSlice, count, next, err := readSliceHeader(body, offset, "records")
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if nilSlice {
		return nil, offset, nil
	}
	records := make([]ch.Record, count)
	for i := range records {
		if records[i], offset, err = readRecord(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return records, offset, nil
}

func appendRecord(dst []byte, record ch.Record) []byte {
	dst = appendUvarint(dst, record.ID)
	dst = appendUvarint(dst, record.Index)
	dst = appendUvarint(dst, record.Epoch)
	dst = appendOptionalBytes(dst, record.Payload)
	dst = appendVarint(dst, int64(record.SizeBytes))
	return dst
}

func readRecord(body []byte, offset int) (ch.Record, int, error) {
	var record ch.Record
	var err error
	if record.ID, offset, err = readUvarint(body, offset); err != nil {
		return ch.Record{}, offset, err
	}
	if record.Index, offset, err = readUvarint(body, offset); err != nil {
		return ch.Record{}, offset, err
	}
	if record.Epoch, offset, err = readUvarint(body, offset); err != nil {
		return ch.Record{}, offset, err
	}
	if record.Payload, offset, err = readOptionalBytes(body, offset, "record payload"); err != nil {
		return ch.Record{}, offset, err
	}
	if record.SizeBytes, offset, err = readInt(body, offset, "record size bytes"); err != nil {
		return ch.Record{}, offset, err
	}
	return record, offset, nil
}

func appendRPCApplicationError(dst []byte, err rpcApplicationError) []byte {
	dst = appendString(dst, err.Code)
	return appendString(dst, err.Message)
}

func readRPCApplicationError(body []byte, offset int) (rpcApplicationError, int, error) {
	var err rpcApplicationError
	var readErr error
	if err.Code, offset, readErr = readString(body, offset); readErr != nil {
		return rpcApplicationError{}, offset, readErr
	}
	if err.Message, offset, readErr = readString(body, offset); readErr != nil {
		return rpcApplicationError{}, offset, readErr
	}
	return err, offset, nil
}

func appendOptionalRPCApplicationError(dst []byte, err error) []byte {
	if err == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendRPCApplicationError(dst, rpcApplicationError{Code: rpcErrorCode(err), Message: err.Error()})
}

func readOptionalRPCApplicationError(body []byte, offset int) (error, int, error) {
	present, next, err := readByte(body, offset, "application error presence")
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if present == 0 {
		return nil, offset, nil
	}
	if present != 1 {
		return nil, offset, fmt.Errorf("channels: invalid application error presence")
	}
	appErr, next, err := readRPCApplicationError(body, offset)
	if err != nil {
		return nil, offset, err
	}
	return decodeRPCApplicationError(appErr), next, nil
}

func rpcErrorCode(err error) string {
	switch {
	case errors.Is(err, ch.ErrInvalidConfig):
		return rpcErrorInvalidConfig
	case errors.Is(err, ch.ErrBackpressured):
		return rpcErrorBackpressured
	case errors.Is(err, ch.ErrNotLeader):
		return rpcErrorNotLeader
	case errors.Is(err, ch.ErrNotReady):
		return rpcErrorNotReady
	case errors.Is(err, ch.ErrStaleMeta):
		return rpcErrorStaleMeta
	case errors.Is(err, ch.ErrChannelNotFound):
		return rpcErrorChannelNotFound
	case errors.Is(err, ch.ErrNotReplica):
		return rpcErrorNotReplica
	case errors.Is(err, ch.ErrClosed):
		return rpcErrorClosed
	case errors.Is(err, ch.ErrTooManyChannels):
		return rpcErrorTooManyChannels
	default:
		return rpcErrorUnknown
	}
}

func decodeRPCApplicationError(err rpcApplicationError) error {
	switch err.Code {
	case rpcErrorInvalidConfig:
		return wrapRPCApplicationError(ch.ErrInvalidConfig, err.Message)
	case rpcErrorBackpressured:
		return wrapRPCApplicationError(ch.ErrBackpressured, err.Message)
	case rpcErrorNotLeader:
		return wrapRPCApplicationError(ch.ErrNotLeader, err.Message)
	case rpcErrorNotReady:
		return wrapRPCApplicationError(ch.ErrNotReady, err.Message)
	case rpcErrorStaleMeta:
		return wrapRPCApplicationError(ch.ErrStaleMeta, err.Message)
	case rpcErrorChannelNotFound:
		return wrapRPCApplicationError(ch.ErrChannelNotFound, err.Message)
	case rpcErrorNotReplica:
		return wrapRPCApplicationError(ch.ErrNotReplica, err.Message)
	case rpcErrorClosed:
		return wrapRPCApplicationError(ch.ErrClosed, err.Message)
	case rpcErrorTooManyChannels:
		return wrapRPCApplicationError(ch.ErrTooManyChannels, err.Message)
	default:
		if err.Message != "" {
			return errors.New(err.Message)
		}
		return errors.New("channels: remote application error")
	}
}

func wrapRPCApplicationError(sentinel error, message string) error {
	if message == "" || message == sentinel.Error() {
		return sentinel
	}
	detail := strings.TrimPrefix(message, sentinel.Error()+": ")
	return fmt.Errorf("%w: %s", sentinel, detail)
}

func appendSliceHeader(dst []byte, length int, nilSlice bool) []byte {
	if nilSlice {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendUvarint(dst, uint64(length))
}

func readSliceHeader(body []byte, offset int, label string) (bool, int, int, error) {
	present, next, err := readByte(body, offset, label+" presence")
	if err != nil {
		return false, 0, offset, err
	}
	offset = next
	if present == 0 {
		return true, 0, offset, nil
	}
	if present != 1 {
		return false, 0, offset, fmt.Errorf("channels: invalid %s presence", label)
	}
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return false, 0, offset, err
	}
	length, err := readCollectionLen(count, len(body)-next, label)
	if err != nil {
		return false, 0, offset, err
	}
	return false, length, next, nil
}

func appendOptionalBytes(dst []byte, value []byte) []byte {
	if value == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendBytes(dst, value)
}

func readOptionalBytes(body []byte, offset int, label string) ([]byte, int, error) {
	present, next, err := readByte(body, offset, label+" presence")
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if present == 0 {
		return nil, offset, nil
	}
	if present != 1 {
		return nil, offset, fmt.Errorf("channels: invalid %s presence", label)
	}
	return readBytesCopy(body, offset, label)
}

func appendTime(dst []byte, value time.Time) []byte {
	if value.IsZero() {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendVarint(dst, value.UnixNano())
}

func readTime(body []byte, offset int) (time.Time, int, error) {
	present, next, err := readByte(body, offset, "time presence")
	if err != nil {
		return time.Time{}, offset, err
	}
	offset = next
	if present == 0 {
		return time.Time{}, offset, nil
	}
	if present != 1 {
		return time.Time{}, offset, fmt.Errorf("channels: invalid time presence")
	}
	nanos, next, err := readVarint(body, offset)
	if err != nil {
		return time.Time{}, offset, err
	}
	return time.Unix(0, nanos), next, nil
}

func appendBool(dst []byte, value bool) []byte {
	if value {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func readBool(body []byte, offset int, label string) (bool, int, error) {
	value, next, err := readByte(body, offset, label)
	if err != nil {
		return false, offset, err
	}
	switch value {
	case 0:
		return false, next, nil
	case 1:
		return true, next, nil
	default:
		return false, offset, fmt.Errorf("channels: invalid %s bool", label)
	}
}

func appendString(dst []byte, value string) []byte {
	dst = appendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func readString(body []byte, offset int) (string, int, error) {
	value, next, err := readBytes(body, offset, "string")
	if err != nil {
		return "", offset, err
	}
	return string(value), next, nil
}

func appendBytes(dst []byte, value []byte) []byte {
	dst = appendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func readBytesCopy(body []byte, offset int, label string) ([]byte, int, error) {
	value, next, err := readBytes(body, offset, label)
	if err != nil {
		return nil, offset, err
	}
	return append([]byte(nil), value...), next, nil
}

func readBytes(body []byte, offset int, label string) ([]byte, int, error) {
	length, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	end := offset + int(length)
	if end < offset || end > len(body) {
		return nil, offset, fmt.Errorf("channels: short %s bytes", label)
	}
	return body[offset:end], end, nil
}

func appendUvarint(dst []byte, value uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], value)
	return append(dst, buf[:n]...)
}

func readUvarint(body []byte, offset int) (uint64, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("channels: short uvarint")
	}
	value, n := binary.Uvarint(body[offset:])
	if n <= 0 {
		return 0, offset, fmt.Errorf("channels: invalid uvarint")
	}
	return value, offset + n, nil
}

func appendVarint(dst []byte, value int64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutVarint(buf[:], value)
	return append(dst, buf[:n]...)
}

func readVarint(body []byte, offset int) (int64, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("channels: short varint")
	}
	value, n := binary.Varint(body[offset:])
	if n <= 0 {
		return 0, offset, fmt.Errorf("channels: invalid varint")
	}
	return value, offset + n, nil
}

func readInt(body []byte, offset int, label string) (int, int, error) {
	value, next, err := readVarint(body, offset)
	if err != nil {
		return 0, offset, err
	}
	maxInt := int64(int(^uint(0) >> 1))
	minInt := -maxInt - 1
	if value > maxInt || value < minInt {
		return 0, offset, fmt.Errorf("channels: %s overflows int", label)
	}
	return int(value), next, nil
}

func readByte(body []byte, offset int, label string) (byte, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("channels: short %s", label)
	}
	return body[offset], offset + 1, nil
}

func readCollectionLen(count uint64, remaining int, label string) (int, error) {
	maxInt := uint64(^uint(0) >> 1)
	if count > maxInt {
		return 0, fmt.Errorf("channels: %s count overflows int", label)
	}
	if count > uint64(remaining) {
		return 0, fmt.Errorf("channels: %s count exceeds remaining bytes", label)
	}
	return int(count), nil
}
