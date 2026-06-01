package channels

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

const codecVersion = uint8(1)

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
	return encode(kindPull, req)
}

// DecodePullRequest decodes a ChannelV2 pull request.
func DecodePullRequest(data []byte) (channeltransport.PullRequest, error) {
	var req channeltransport.PullRequest
	return req, decode(data, kindPull, &req)
}

func encodePullResponse(resp channeltransport.PullResponse) ([]byte, error) {
	return encodeRPCResult(kindPullResponse, resp, nil)
}
func decodePullResponse(data []byte) (channeltransport.PullResponse, error) {
	var resp channeltransport.PullResponse
	return resp, decodeRPCResult(data, kindPullResponse, &resp)
}
func encodeAckRequest(req channeltransport.AckRequest) ([]byte, error) { return encode(kindAck, req) }
func decodeAckRequest(data []byte) (channeltransport.AckRequest, error) {
	var req channeltransport.AckRequest
	return req, decode(data, kindAck, &req)
}
func encodePullHintRequest(req channeltransport.PullHintRequest) ([]byte, error) {
	return encode(kindPullHint, req)
}
func decodePullHintRequest(data []byte) (channeltransport.PullHintRequest, error) {
	var req channeltransport.PullHintRequest
	return req, decode(data, kindPullHint, &req)
}
func encodeNotifyRequest(req channeltransport.NotifyRequest) ([]byte, error) {
	return encode(kindNotify, req)
}
func decodeNotifyRequest(data []byte) (channeltransport.NotifyRequest, error) {
	var req channeltransport.NotifyRequest
	return req, decode(data, kindNotify, &req)
}
func encodeAppendRequest(req ch.AppendRequest) ([]byte, error) { return encode(kindAppend, req) }
func decodeAppendRequest(data []byte) (ch.AppendRequest, error) {
	var req ch.AppendRequest
	return req, decode(data, kindAppend, &req)
}
func encodeAppendResponse(resp ch.AppendResult) ([]byte, error) {
	return encodeRPCResult(kindAppendResponse, resp, nil)
}
func decodeAppendResponse(data []byte) (ch.AppendResult, error) {
	var resp ch.AppendResult
	return resp, decodeRPCResult(data, kindAppendResponse, &resp)
}
func encodeAppendBatchRequest(req ch.AppendBatchRequest) ([]byte, error) {
	return encode(kindAppendBatch, req)
}
func decodeAppendBatchRequest(data []byte) (ch.AppendBatchRequest, error) {
	var req ch.AppendBatchRequest
	return req, decode(data, kindAppendBatch, &req)
}
func encodeAppendBatchResponse(resp ch.AppendBatchResult) ([]byte, error) {
	return encodeRPCResult(kindAppendBatchResponse, resp, nil)
}
func decodeAppendBatchResponse(data []byte) (ch.AppendBatchResult, error) {
	var resp ch.AppendBatchResult
	return resp, decodeRPCResult(data, kindAppendBatchResponse, &resp)
}

type rpcResultEnvelope struct {
	Payload json.RawMessage      `json:"payload,omitempty"`
	Error   *rpcApplicationError `json:"error,omitempty"`
}

type rpcApplicationError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
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

func encodeRPCResult(kind uint8, payload any, err error) ([]byte, error) {
	result := rpcResultEnvelope{}
	if err != nil {
		result.Error = &rpcApplicationError{Code: rpcErrorCode(err), Message: err.Error()}
		return encode(kind, result)
	}
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		result.Payload = data
	}
	return encode(kind, result)
}

func decodeRPCResult(data []byte, kind uint8, payload any) error {
	var result rpcResultEnvelope
	if err := decode(data, kind, &result); err != nil {
		return err
	}
	if result.Error != nil {
		return decodeRPCApplicationError(*result.Error)
	}
	if payload == nil {
		return nil
	}
	if len(result.Payload) == 0 {
		return fmt.Errorf("channels: missing result payload")
	}
	return json.Unmarshal(result.Payload, payload)
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

func encode(kind uint8, value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	out := []byte{codecVersion, kind}
	return append(out, payload...), nil
}

func decode(data []byte, wantKind uint8, value any) error {
	if len(data) < 2 || data[0] != codecVersion || data[1] != wantKind {
		return fmt.Errorf("channels: invalid frame")
	}
	return json.Unmarshal(data[2:], value)
}
