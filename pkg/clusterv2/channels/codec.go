package channels

import (
	"encoding/json"
	"fmt"

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
	return encode(kindPullResponse, resp)
}
func decodePullResponse(data []byte) (channeltransport.PullResponse, error) {
	var resp channeltransport.PullResponse
	return resp, decode(data, kindPullResponse, &resp)
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
	return encode(kindAppendResponse, resp)
}
func decodeAppendResponse(data []byte) (ch.AppendResult, error) {
	var resp ch.AppendResult
	return resp, decode(data, kindAppendResponse, &resp)
}
func encodeAppendBatchRequest(req ch.AppendBatchRequest) ([]byte, error) {
	return encode(kindAppendBatch, req)
}
func decodeAppendBatchRequest(data []byte) (ch.AppendBatchRequest, error) {
	var req ch.AppendBatchRequest
	return req, decode(data, kindAppendBatch, &req)
}
func encodeAppendBatchResponse(resp ch.AppendBatchResult) ([]byte, error) {
	return encode(kindAppendBatchResponse, resp)
}
func decodeAppendBatchResponse(data []byte) (ch.AppendBatchResult, error) {
	var resp ch.AppendBatchResult
	return resp, decode(data, kindAppendBatchResponse, &resp)
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
