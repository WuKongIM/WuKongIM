package node

import (
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
)

func TestRecipientAuthorityCodecRoundTripRequest(t *testing.T) {
	req := recipientAuthorityRequest{
		Target: authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5},
		Event: messageevents.MessageCommitted{
			MessageID:         1001,
			MessageSeq:        42,
			ChannelID:         "g1",
			ChannelType:       2,
			FromUID:           "u1",
			SenderNodeID:      7,
			SenderSessionID:   8,
			ClientMsgNo:       "client-1",
			ServerTimestampMS: 9000,
			Payload:           []byte("hello"),
			RedDot:            true,
			MessageScopedUIDs: []string{"u2", "u3"},
		},
		Recipients: []recipientusecase.Recipient{{UID: "u2", JoinSeq: 10}, {UID: "u3", JoinSeq: 20}},
	}

	body, err := encodeRecipientAuthorityRequest(req)
	if err != nil {
		t.Fatalf("encodeRecipientAuthorityRequest() error = %v", err)
	}
	got, err := decodeRecipientAuthorityRequest(body)
	if err != nil {
		t.Fatalf("decodeRecipientAuthorityRequest() error = %v", err)
	}

	if !reflect.DeepEqual(got, req) {
		t.Fatalf("decoded request = %#v, want %#v", got, req)
	}
}

func TestRecipientAuthorityCodecRoundTripResponse(t *testing.T) {
	body, err := encodeRecipientAuthorityResponse(recipientAuthorityResponse{Status: rpcStatusStaleRoute})
	if err != nil {
		t.Fatalf("encodeRecipientAuthorityResponse() error = %v", err)
	}
	got, err := decodeRecipientAuthorityResponse(body)
	if err != nil {
		t.Fatalf("decodeRecipientAuthorityResponse() error = %v", err)
	}
	if got.Status != rpcStatusStaleRoute {
		t.Fatalf("status = %q, want %q", got.Status, rpcStatusStaleRoute)
	}
}

func TestRecipientAuthorityCodecRejectsMalformedRequest(t *testing.T) {
	validReq, err := encodeRecipientAuthorityRequest(recipientAuthorityRequest{
		Target:     authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 3},
		Event:      messageevents.MessageCommitted{MessageID: 1, ChannelID: "g1"},
		Recipients: []recipientusecase.Recipient{{UID: "u1"}},
	})
	if err != nil {
		t.Fatalf("encodeRecipientAuthorityRequest() error = %v", err)
	}

	tests := []struct {
		name string
		body []byte
	}{
		{name: "bad magic", body: []byte("bad")},
		{name: "truncated", body: validReq[:len(validReq)-1]},
		{name: "trailing bytes", body: append(append([]byte(nil), validReq...), 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeRecipientAuthorityRequest(tt.body); err == nil {
				t.Fatal("decodeRecipientAuthorityRequest() error = nil, want malformed payload error")
			}
		})
	}
}

func TestRecipientAuthorityCodecRejectsOversizedRecipients(t *testing.T) {
	body := make([]byte, 0, 64+maxRecipientAuthorityCollectionLen+1)
	body = append(body, recipientAuthorityRequestMagic[:]...)
	body = appendAuthorityTarget(body, authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 3})
	body = appendMessageCommitted(body, messageevents.MessageCommitted{MessageID: 1})
	body = appendUvarint(body, uint64(maxRecipientAuthorityCollectionLen+1))
	body = append(body, make([]byte, maxRecipientAuthorityCollectionLen+1)...)

	if _, err := decodeRecipientAuthorityRequest(body); err == nil {
		t.Fatal("decodeRecipientAuthorityRequest() error = nil, want oversized collection error")
	}
}
