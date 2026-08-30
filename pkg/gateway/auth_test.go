package gateway_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

func TestAuthenticatorStoresNegotiatedProtocolVersion(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{DisableEncryption: true})

	result, err := auth.Authenticate(nil, &frame.ConnectPacket{
		Version: 5,
		UID:     "u1",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if result.SessionValues[gateway.SessionValueProtocolVersion] != uint8(5) {
		t.Fatalf("protocol version = %#v, want 5", result.SessionValues[gateway.SessionValueProtocolVersion])
	}
}

func TestAuthenticatorStoresDeviceIDSessionValue(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{DisableEncryption: true})

	result, err := auth.Authenticate(nil, &frame.ConnectPacket{
		UID:      "u1",
		DeviceID: "d-1",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if result.SessionValues[gateway.SessionValueDeviceID] != "d-1" {
		t.Fatalf("device id = %#v, want %q", result.SessionValues[gateway.SessionValueDeviceID], "d-1")
	}
}

func TestAuthenticatorNegotiatesWKProtoEncryption(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
		EncryptionEnabled: true,
	})

	result, err := auth.Authenticate(nil, &frame.ConnectPacket{
		UID:       "u1",
		ClientKey: testClientPublicKey(t),
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if result.Connack.ServerKey == "" {
		t.Fatal("ServerKey is empty")
	}
	if result.Connack.Salt == "" {
		t.Fatal("Salt is empty")
	}
	if got := result.SessionValues[gateway.SessionValueEncryptionEnabled]; got != true {
		t.Fatalf("encryption enabled = %#v, want true", got)
	}
	if _, ok := result.SessionValues[gateway.SessionValueAESKey].([]byte); !ok {
		t.Fatalf("AESKey type = %T, want []byte", result.SessionValues[gateway.SessionValueAESKey])
	}
	if _, ok := result.SessionValues[gateway.SessionValueAESIV].([]byte); !ok {
		t.Fatalf("AESIV type = %T, want []byte", result.SessionValues[gateway.SessionValueAESIV])
	}
	if _, ok := result.SessionValues[gateway.SessionValueCrypto].(*wkprotoenc.SessionCrypto); !ok {
		t.Fatalf("SessionCrypto type = %T, want *wkprotoenc.SessionCrypto", result.SessionValues[gateway.SessionValueCrypto])
	}
}

func TestAuthenticatorRejectsMissingClientKeyWhenEncryptionEnabled(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
		EncryptionEnabled: true,
	})

	result, err := auth.Authenticate(nil, &frame.ConnectPacket{UID: "u1"})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if got, want := result.Connack.ReasonCode, frame.ReasonClientKeyIsEmpty; got != want {
		t.Fatalf("ReasonCode = %v, want %v", got, want)
	}
}

func TestAuthenticatorAllowsJSONRPCWithoutWKProtoKeyNegotiation(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
		EncryptionEnabled: true,
	})

	result, err := auth.Authenticate(&gateway.Context{Protocol: "jsonrpc"}, &frame.ConnectPacket{
		Version:  frame.LatestVersion,
		UID:      "u1",
		DeviceID: "d-1",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if got, want := result.Connack.ReasonCode, frame.ReasonSuccess; got != want {
		t.Fatalf("ReasonCode = %v, want %v", got, want)
	}
	if result.Connack.ServerKey != "" || result.Connack.Salt != "" {
		t.Fatalf("JSON-RPC CONNACK unexpectedly contains WKProto encryption material: %#v", result.Connack)
	}
	if got := result.SessionValues[gateway.SessionValueEncryptionEnabled]; got != nil {
		t.Fatalf("JSON-RPC encryption session value = %#v, want nil", got)
	}
	if got := result.SessionValues[gateway.SessionValueUID]; got != "u1" {
		t.Fatalf("uid session value = %#v, want u1", got)
	}
}

func TestAuthenticatorKeepsTokenAndBanChecksForJSONRPC(t *testing.T) {
	var verified, banned bool
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
		TokenAuthOn: true,
		VerifyToken: func(uid string, deviceFlag frame.DeviceFlag, token string) (frame.DeviceLevel, error) {
			verified = true
			if uid != "u1" || deviceFlag != frame.APP || token != "token-1" {
				t.Fatalf("VerifyToken(%q, %v, %q)", uid, deviceFlag, token)
			}
			return frame.DeviceLevelMaster, nil
		},
		IsBanned: func(uid string) (bool, error) {
			banned = true
			if uid != "u1" {
				t.Fatalf("IsBanned(%q)", uid)
			}
			return false, nil
		},
	})

	result, err := auth.Authenticate(&gateway.Context{Protocol: "jsonrpc"}, &frame.ConnectPacket{
		UID:        "u1",
		DeviceFlag: frame.APP,
		Token:      "token-1",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if result.Connack.ReasonCode != frame.ReasonSuccess || !verified || !banned {
		t.Fatalf("JSON-RPC auth result = %#v, verified=%v banned=%v", result.Connack, verified, banned)
	}
	if got := result.SessionValues[gateway.SessionValueDeviceLevel]; got != frame.DeviceLevelMaster {
		t.Fatalf("device level = %#v, want master", got)
	}
}

func TestAuthenticatorDoesNotInventJSONRPCClientTimeDiff(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
		Now: func() time.Time { return now },
	})

	jsonResult, err := auth.Authenticate(&gateway.Context{Protocol: "jsonrpc"}, &frame.ConnectPacket{UID: "u1"})
	if err != nil {
		t.Fatalf("Authenticate(jsonrpc) error = %v", err)
	}
	if got := jsonResult.Connack.TimeDiff; got != 0 {
		t.Fatalf("JSON-RPC TimeDiff = %d, want 0 when clientTimestamp is absent", got)
	}

	wkResult, err := auth.Authenticate(&gateway.Context{Protocol: "wkproto"}, &frame.ConnectPacket{
		UID:       "u1",
		ClientKey: testClientPublicKey(t),
	})
	if err != nil {
		t.Fatalf("Authenticate(wkproto) error = %v", err)
	}
	if got := wkResult.Connack.TimeDiff; got != now.UnixMilli() {
		t.Fatalf("WKProto TimeDiff = %d, want %d", got, now.UnixMilli())
	}
}

func TestAuthenticatorRejectsInvalidClientKeyWhenEncryptionEnabled(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
		EncryptionEnabled: true,
	})

	result, err := auth.Authenticate(nil, &frame.ConnectPacket{
		UID:       "u1",
		ClientKey: "bad-client-key",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if got, want := result.Connack.ReasonCode, frame.ReasonAuthFail; got != want {
		t.Fatalf("ReasonCode = %v, want %v", got, want)
	}
}

func TestAuthenticatorSkipsEncryptionMaterialWhenDisabled(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
		DisableEncryption: true,
	})

	result, err := auth.Authenticate(nil, &frame.ConnectPacket{
		UID:       "u1",
		ClientKey: testClientPublicKey(t),
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if result.Connack.ServerKey != "" {
		t.Fatalf("ServerKey = %q, want empty", result.Connack.ServerKey)
	}
	if result.Connack.Salt != "" {
		t.Fatalf("Salt = %q, want empty", result.Connack.Salt)
	}
	if got := result.SessionValues[gateway.SessionValueEncryptionEnabled]; got != nil {
		t.Fatalf("encryption enabled = %#v, want nil", got)
	}
}

func testClientPublicKey(t *testing.T) string {
	t.Helper()

	_, public, err := wkprotoenc.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	return wkprotoenc.EncodePublicKey(public)
}
