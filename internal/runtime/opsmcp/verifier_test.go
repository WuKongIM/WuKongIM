package opsmcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
)

func TestVerifierAcceptsOnlyEnabledCurrentCredentialDigest(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	token := "wko_credential_1_" + secret
	sum := sha256.Sum256([]byte(token))
	reader := stateReaderStub{state: DesiredState{
		Revision: 9, Enabled: true, OwnerNodeID: 2,
		Credentials: []Credential{{ID: "credential_1", DigestSHA256: hex.EncodeToString(sum[:])}},
	}}
	verifier := NewVerifier(reader)
	got, err := verifier.VerifyBearer(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyBearer() error = %v", err)
	}
	if got.CredentialID != "credential_1" || got.Revision != 9 {
		t.Fatalf("principal = %#v", got)
	}
	if got.OwnerNodeID != 2 || got.DigestSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("principal forwarding fields = %#v", got)
	}
	for _, invalid := range []string{
		"manager.jwt.value",
		"wko_credential_1_wrong",
		"wko_other_" + secret,
		token + " ",
	} {
		if _, err := verifier.VerifyBearer(context.Background(), invalid); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("VerifyBearer(%q) error = %v, want unauthorized", invalid, err)
		}
	}
}

func TestVerifierRevalidatesForwardOnConfiguredOwner(t *testing.T) {
	sum := sha256.Sum256([]byte("token"))
	reader := stateReaderStub{state: DesiredState{
		Revision: 11, Enabled: true, OwnerNodeID: 3,
		Credentials: []Credential{{ID: "credential", DigestSHA256: hex.EncodeToString(sum[:])}},
	}}
	verifier := NewNodeVerifier(reader, 3)
	got, err := verifier.VerifyForward(context.Background(), "credential", hex.EncodeToString(sum[:]), 11)
	if err != nil || got.OwnerNodeID != 3 {
		t.Fatalf("VerifyForward() principal=%#v error=%v", got, err)
	}
	if _, err := verifier.VerifyForward(context.Background(), "credential", hex.EncodeToString(sum[:]), 10); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("stale revision error = %v", err)
	}
	if _, err := NewNodeVerifier(reader, 2).VerifyForward(context.Background(), "credential", hex.EncodeToString(sum[:]), 11); !errors.Is(err, ErrOwnerUnavailable) {
		t.Fatalf("wrong owner error = %v", err)
	}
}

func TestVerifierReportsDisabledAndUnavailableSeparately(t *testing.T) {
	disabled := NewVerifier(stateReaderStub{state: DesiredState{Enabled: false}})
	if _, err := disabled.VerifyBearer(context.Background(), "anything"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled error = %v", err)
	}
	unavailable := NewVerifier(stateReaderStub{err: errors.New("snapshot failed")})
	if _, err := unavailable.VerifyBearer(context.Background(), "anything"); !errors.Is(err, ErrOwnerUnavailable) {
		t.Fatalf("unavailable error = %v", err)
	}
}

func TestParseTokenAcceptsBase64URLSecretContainingUnderscore(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString([]byte{
		255, 255, 255, 255, 255, 255, 255, 255,
		255, 255, 255, 255, 255, 255, 255, 255,
		255, 255, 255, 255, 255, 255, 255, 255,
		255, 255, 255, 255, 255, 255, 255, 255,
	})
	raw := "wko_credential_1_" + secret
	credentialID, ok := parseToken(raw)
	if !ok || credentialID != "credential_1" {
		t.Fatalf("parseToken() = %q, %v for secret %q", credentialID, ok, secret)
	}
}

type stateReaderStub struct {
	state DesiredState
	err   error
}

func (s stateReaderStub) OpsMCPDesiredState(context.Context) (DesiredState, error) {
	return s.state, s.err
}
