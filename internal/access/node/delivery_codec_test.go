package node

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"testing"

	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
)

func TestDeliveryPushCodecPreservesLegacyWireBytes(t *testing.T) {
	body, err := encodeDeliveryPushRequest(deliveryPushRequest{Command: testDeliveryPushCommand()})
	if err != nil {
		t.Fatalf("encodeDeliveryPushRequest() error = %v", err)
	}
	const legacyHex = "574b5644010de90707096368616e6e656c2d31020673656e646572032108636c69656e742d3101040102030402027531027532020275310d17cd0865096465766963652d753101020275320d17b209ca01096465766963652d75320102"
	want, err := hex.DecodeString(legacyHex)
	if err != nil {
		t.Fatalf("DecodeString(legacy) error = %v", err)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("delivery push wire bytes = %x, want legacy %x", body, want)
	}
}

func TestDeliveryCodecRequestRoundTrip(t *testing.T) {
	req := deliveryPushRequest{Command: testDeliveryPushCommand()}

	body, err := encodeDeliveryPushRequest(req)
	if err != nil {
		t.Fatalf("encodeDeliveryPushRequest() error = %v", err)
	}
	again, err := encodeDeliveryPushRequest(req)
	if err != nil {
		t.Fatalf("second encodeDeliveryPushRequest() error = %v", err)
	}
	if !bytes.Equal(body, again) {
		t.Fatal("encodeDeliveryPushRequest() is not deterministic")
	}

	got, err := decodeDeliveryPushRequest(body)
	if err != nil {
		t.Fatalf("decodeDeliveryPushRequest() error = %v", err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("decodeDeliveryPushRequest() = %#v, want %#v", got, req)
	}

	body[0] = 'X'
	body[len(body)-1] ^= 0xff
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("decoded request changed after body mutation: %#v", got)
	}
}

func TestDeliveryCodecResponseRoundTrip(t *testing.T) {
	resp := deliveryPushResponse{
		Status: rpcStatusOK,
		Result: onlinedelivery.OwnerPushResult{
			Accepted:  []onlinedelivery.Route{testDeliveryRoute("u1", 101)},
			Retryable: []onlinedelivery.Route{testDeliveryRoute("u2", 202)},
			Dropped:   []onlinedelivery.Route{testDeliveryRoute("u3", 303)},
		},
	}

	body, err := encodeDeliveryPushResponse(resp)
	if err != nil {
		t.Fatalf("encodeDeliveryPushResponse() error = %v", err)
	}
	again, err := encodeDeliveryPushResponse(resp)
	if err != nil {
		t.Fatalf("second encodeDeliveryPushResponse() error = %v", err)
	}
	if !bytes.Equal(body, again) {
		t.Fatal("encodeDeliveryPushResponse() is not deterministic")
	}

	got, err := decodeDeliveryPushResponse(body)
	if err != nil {
		t.Fatalf("decodeDeliveryPushResponse() error = %v", err)
	}
	if !reflect.DeepEqual(got, resp) {
		t.Fatalf("decodeDeliveryPushResponse() = %#v, want %#v", got, resp)
	}
}

func TestDeliveryCodecRejectsBadMagicTruncatedAndTrailingBytes(t *testing.T) {
	reqBody, err := encodeDeliveryPushRequest(deliveryPushRequest{Command: testDeliveryPushCommand()})
	if err != nil {
		t.Fatalf("encodeDeliveryPushRequest() error = %v", err)
	}
	respBody, err := encodeDeliveryPushResponse(deliveryPushResponse{Status: rpcStatusOK})
	if err != nil {
		t.Fatalf("encodeDeliveryPushResponse() error = %v", err)
	}

	badReqMagic := append([]byte(nil), reqBody...)
	badReqMagic[0] = 'X'
	if _, err := decodeDeliveryPushRequest(badReqMagic); err == nil {
		t.Fatal("decodeDeliveryPushRequest() accepted bad magic")
	}
	if _, err := decodeDeliveryPushRequest(reqBody[:len(reqBody)-1]); err == nil {
		t.Fatal("decodeDeliveryPushRequest() accepted truncated body")
	}
	if _, err := decodeDeliveryPushRequest(append(append([]byte(nil), reqBody...), 0)); err == nil {
		t.Fatal("decodeDeliveryPushRequest() accepted trailing bytes")
	}

	badRespMagic := append([]byte(nil), respBody...)
	badRespMagic[0] = 'X'
	if _, err := decodeDeliveryPushResponse(badRespMagic); err == nil {
		t.Fatal("decodeDeliveryPushResponse() accepted bad magic")
	}
	if _, err := decodeDeliveryPushResponse(respBody[:len(respBody)-1]); err == nil {
		t.Fatal("decodeDeliveryPushResponse() accepted truncated body")
	}
	if _, err := decodeDeliveryPushResponse(append(append([]byte(nil), respBody...), 0)); err == nil {
		t.Fatal("decodeDeliveryPushResponse() accepted trailing bytes")
	}
}

func testDeliveryPushCommand() onlinedelivery.OwnerPush {
	return onlinedelivery.OwnerPush{
		OwnerNodeID: 13,
		Event: channelappendcontract.CommittedEnvelope{
			MessageID:         1001,
			MessageSeq:        7,
			ChannelID:         "channel-1",
			ChannelType:       2,
			FromUID:           "sender",
			SenderNodeID:      3,
			SenderSessionID:   33,
			ClientMsgNo:       "client-1",
			RedDot:            true,
			Payload:           []byte{1, 2, 3, 4},
			MessageScopedUIDs: []string{"u1", "u2"},
		},
		Routes: []onlinedelivery.Route{
			testDeliveryRoute("u1", 101),
			testDeliveryRoute("u2", 202),
		},
	}
}

func testDeliveryRoute(uid string, sessionID uint64) onlinedelivery.Route {
	return onlinedelivery.Route{
		UID:         uid,
		OwnerNodeID: 13,
		OwnerBootID: 23,
		OwnerSeq:    sessionID + 1000,
		SessionID:   sessionID,
		DeviceID:    "device-" + uid,
		DeviceFlag:  1,
		DeviceLevel: 2,
	}
}
