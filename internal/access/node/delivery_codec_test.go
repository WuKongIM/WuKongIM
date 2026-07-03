package node

import (
	"bytes"
	"reflect"
	"testing"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

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
		Result: runtimedelivery.PushResult{
			Accepted:  []runtimedelivery.Route{testDeliveryRoute("u1", 101)},
			Retryable: []runtimedelivery.Route{testDeliveryRoute("u2", 202)},
			Dropped:   []runtimedelivery.Route{testDeliveryRoute("u3", 303)},
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

func TestDeliveryFanoutCodecRoundTrip(t *testing.T) {
	req := deliveryFanoutRequest{Task: testDeliveryFanoutTask()}

	body, err := encodeDeliveryFanoutRequest(req)
	if err != nil {
		t.Fatalf("encodeDeliveryFanoutRequest() error = %v", err)
	}
	again, err := encodeDeliveryFanoutRequest(req)
	if err != nil {
		t.Fatalf("second encodeDeliveryFanoutRequest() error = %v", err)
	}
	if !bytes.Equal(body, again) {
		t.Fatal("encodeDeliveryFanoutRequest() is not deterministic")
	}
	got, err := decodeDeliveryFanoutRequest(body)
	if err != nil {
		t.Fatalf("decodeDeliveryFanoutRequest() error = %v", err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("decodeDeliveryFanoutRequest() = %#v, want %#v", got, req)
	}

	resp := deliveryFanoutResponse{Status: rpcStatusOK}
	respBody, err := encodeDeliveryFanoutResponse(resp)
	if err != nil {
		t.Fatalf("encodeDeliveryFanoutResponse() error = %v", err)
	}
	decodedResp, err := decodeDeliveryFanoutResponse(respBody)
	if err != nil {
		t.Fatalf("decodeDeliveryFanoutResponse() error = %v", err)
	}
	if !reflect.DeepEqual(decodedResp, resp) {
		t.Fatalf("decodeDeliveryFanoutResponse() = %#v, want %#v", decodedResp, resp)
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

func TestDeliveryFanoutCodecRejectsBadMagicTruncatedAndTrailingBytes(t *testing.T) {
	reqBody, err := encodeDeliveryFanoutRequest(deliveryFanoutRequest{Task: testDeliveryFanoutTask()})
	if err != nil {
		t.Fatalf("encodeDeliveryFanoutRequest() error = %v", err)
	}
	respBody, err := encodeDeliveryFanoutResponse(deliveryFanoutResponse{Status: rpcStatusOK})
	if err != nil {
		t.Fatalf("encodeDeliveryFanoutResponse() error = %v", err)
	}

	badReqMagic := append([]byte(nil), reqBody...)
	badReqMagic[0] = 'X'
	if _, err := decodeDeliveryFanoutRequest(badReqMagic); err == nil {
		t.Fatal("decodeDeliveryFanoutRequest() accepted bad magic")
	}
	if _, err := decodeDeliveryFanoutRequest(reqBody[:len(reqBody)-1]); err == nil {
		t.Fatal("decodeDeliveryFanoutRequest() accepted truncated body")
	}
	if _, err := decodeDeliveryFanoutRequest(append(append([]byte(nil), reqBody...), 0)); err == nil {
		t.Fatal("decodeDeliveryFanoutRequest() accepted trailing bytes")
	}

	badRespMagic := append([]byte(nil), respBody...)
	badRespMagic[0] = 'X'
	if _, err := decodeDeliveryFanoutResponse(badRespMagic); err == nil {
		t.Fatal("decodeDeliveryFanoutResponse() accepted bad magic")
	}
	if _, err := decodeDeliveryFanoutResponse(respBody[:len(respBody)-1]); err == nil {
		t.Fatal("decodeDeliveryFanoutResponse() accepted truncated body")
	}
	if _, err := decodeDeliveryFanoutResponse(append(append([]byte(nil), respBody...), 0)); err == nil {
		t.Fatal("decodeDeliveryFanoutResponse() accepted trailing bytes")
	}
}

func testDeliveryPushCommand() runtimedelivery.PushCommand {
	return runtimedelivery.PushCommand{
		OwnerNodeID: 13,
		Envelope: runtimedelivery.Envelope{
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
		Routes: []runtimedelivery.Route{
			testDeliveryRoute("u1", 101),
			testDeliveryRoute("u2", 202),
		},
	}
}

func testDeliveryRoute(uid string, sessionID uint64) runtimedelivery.Route {
	return runtimedelivery.Route{
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
