package gateway_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/wkprotoenc"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
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
