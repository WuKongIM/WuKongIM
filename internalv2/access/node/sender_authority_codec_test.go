package node

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
)

func TestSenderAuthorityCodecRoundTripRequest(t *testing.T) {
	req := senderAuthorityRequest{
		Target: authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5},
		Items: []senderAuthorityItem{{
			Command: senderAuthorityTestCommand(),
			Timeout: 250 * time.Millisecond,
		}},
	}

	body, err := encodeSenderAuthorityRequest(req)
	if err != nil {
		t.Fatalf("encodeSenderAuthorityRequest() error = %v", err)
	}
	got, err := decodeSenderAuthorityRequest(body)
	if err != nil {
		t.Fatalf("decodeSenderAuthorityRequest() error = %v", err)
	}

	if !reflect.DeepEqual(got.Target, req.Target) {
		t.Fatalf("target = %#v, want %#v", got.Target, req.Target)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(got.Items))
	}
	if got.Items[0].Timeout != req.Items[0].Timeout {
		t.Fatalf("timeout = %s, want %s", got.Items[0].Timeout, req.Items[0].Timeout)
	}
	if !reflect.DeepEqual(got.Items[0].Command, req.Items[0].Command) {
		t.Fatalf("command = %#v, want %#v", got.Items[0].Command, req.Items[0].Command)
	}
}

func TestSenderAuthorityCodecRoundTripResponse(t *testing.T) {
	resp := senderAuthorityResponse{
		Status: rpcStatusOK,
		Results: []message.SendBatchItemResult{
			{
				Result: message.SendResult{
					MessageID:  1001,
					MessageSeq: 22,
					Reason:     message.ReasonSuccess,
				},
			},
			{
				Result: message.SendResult{
					MessageID:  1002,
					MessageSeq: 23,
					Reason:     message.ReasonNodeNotMatch,
				},
				Err: message.ErrNotLeader,
			},
		},
	}

	body, err := encodeSenderAuthorityResponse(resp)
	if err != nil {
		t.Fatalf("encodeSenderAuthorityResponse() error = %v", err)
	}
	got, err := decodeSenderAuthorityResponse(body)
	if err != nil {
		t.Fatalf("decodeSenderAuthorityResponse() error = %v", err)
	}

	if got.Status != resp.Status {
		t.Fatalf("status = %q, want %q", got.Status, resp.Status)
	}
	if len(got.Results) != 2 {
		t.Fatalf("results len = %d, want 2", len(got.Results))
	}
	if got.Results[0].Result != resp.Results[0].Result {
		t.Fatalf("success result = %#v, want %#v", got.Results[0].Result, resp.Results[0].Result)
	}
	if got.Results[1].Result != resp.Results[1].Result {
		t.Fatalf("error result = %#v, want %#v", got.Results[1].Result, resp.Results[1].Result)
	}
	if !errors.Is(got.Results[1].Err, message.ErrNotLeader) {
		t.Fatalf("error = %v, want ErrNotLeader", got.Results[1].Err)
	}
}

func TestSenderAuthorityCodecRoundTripContextErrors(t *testing.T) {
	resp := senderAuthorityResponse{
		Status: rpcStatusOK,
		Results: []message.SendBatchItemResult{
			{Err: context.Canceled},
			{Err: context.DeadlineExceeded},
		},
	}

	body, err := encodeSenderAuthorityResponse(resp)
	if err != nil {
		t.Fatalf("encodeSenderAuthorityResponse() error = %v", err)
	}
	got, err := decodeSenderAuthorityResponse(body)
	if err != nil {
		t.Fatalf("decodeSenderAuthorityResponse() error = %v", err)
	}

	if len(got.Results) != 2 {
		t.Fatalf("results len = %d, want 2", len(got.Results))
	}
	if !errors.Is(got.Results[0].Err, context.Canceled) {
		t.Fatalf("result[0].Err = %v, want context.Canceled", got.Results[0].Err)
	}
	if !errors.Is(got.Results[1].Err, context.DeadlineExceeded) {
		t.Fatalf("result[1].Err = %v, want context.DeadlineExceeded", got.Results[1].Err)
	}
}

func TestSenderAuthorityCodecRejectsMalformedRequest(t *testing.T) {
	validReq, err := encodeSenderAuthorityRequest(senderAuthorityRequest{
		Target: authority.Target{LeaderNodeID: 1},
		Items:  []senderAuthorityItem{{Command: senderAuthorityTestCommand()}},
	})
	if err != nil {
		t.Fatalf("encodeSenderAuthorityRequest() error = %v", err)
	}

	tests := []struct {
		name string
		body []byte
	}{
		{name: "invalid magic", body: []byte("bad")},
		{name: "truncated", body: validReq[:len(validReq)-1]},
		{name: "trailing bytes", body: append(append([]byte(nil), validReq...), 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeSenderAuthorityRequest(tt.body); err == nil {
				t.Fatal("decodeSenderAuthorityRequest() error = nil, want malformed payload error")
			}
		})
	}
}

func TestSenderAuthorityCodecRejectsOversizedItems(t *testing.T) {
	body := make([]byte, 0, 64+maxSenderAuthorityCollectionLen+1)
	body = append(body, senderAuthorityRequestMagic[:]...)
	body = appendAuthorityTarget(body, authority.Target{LeaderNodeID: 1})
	body = appendUvarint(body, uint64(maxSenderAuthorityCollectionLen+1))
	body = append(body, make([]byte, maxSenderAuthorityCollectionLen+1)...)

	if _, err := decodeSenderAuthorityRequest(body); err == nil {
		t.Fatal("decodeSenderAuthorityRequest() error = nil, want oversized collection error")
	}
}

func senderAuthorityTestCommand() message.SendCommand {
	return message.SendCommand{
		FromUID:                "u1",
		SenderNodeID:           11,
		SenderSessionID:        12,
		ClientSeq:              13,
		ClientMsgNo:            "client-1",
		TraceID:                "trace-1",
		ChannelKey:             "channel-key",
		ChannelID:              "g1",
		ChannelType:            2,
		Payload:                []byte("hello"),
		NoPersist:              true,
		SyncOnce:               true,
		RedDot:                 true,
		NormalizePersonChannel: true,
		RequestScoped:          true,
		MessageScopedUIDs:      []string{"u2", "u3"},
		MessageID:              99,
		ProtocolVersion:        4,
	}
}
