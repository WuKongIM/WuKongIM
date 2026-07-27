// Package opsmcp owns authentication, limits, cache, audit, and active profile
// coordination for the embedded operations MCP.
package opsmcp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

var (
	// ErrUnauthorized reports an absent or invalid MCP bearer token.
	ErrUnauthorized = errors.New("internal/runtime/opsmcp: unauthorized")
	// ErrDisabled reports that Controller desired state has MCP stopped.
	ErrDisabled = errors.New("internal/runtime/opsmcp: disabled")
	// ErrOwnerUnavailable reports that desired state cannot currently be read or executed.
	ErrOwnerUnavailable = errors.New("internal/runtime/opsmcp: owner unavailable")
	// ErrStateChanged reports that an ingress-authenticated Controller revision is stale.
	ErrStateChanged = errors.New("internal/runtime/opsmcp: desired state changed")
)

// StateReader returns the latest Controller-derived MCP desired state.
type StateReader interface {
	// OpsMCPDesiredState returns one immutable desired-state snapshot.
	OpsMCPDesiredState(context.Context) (DesiredState, error)
}

// DesiredState is the narrow runtime projection of Controller state.
type DesiredState struct {
	// Revision is the global Controller revision.
	Revision uint64
	// Enabled is the durable desired serving state.
	Enabled bool
	// OwnerNodeID is the single execution owner.
	OwnerNodeID uint64
	// ProfileFenceUntilUnixMillis prevents capture during an owner transition.
	ProfileFenceUntilUnixMillis int64
	// Credentials contains bounded one-way token verifiers.
	Credentials []Credential
}

// Credential is one non-secret token verifier.
type Credential struct {
	// ID is the identifier encoded into the opaque token.
	ID string
	// DigestSHA256 is the lowercase digest of the complete token.
	DigestSHA256 string
}

// Principal is the bounded identity established from one MCP bearer token.
type Principal struct {
	// CredentialID is the non-secret credential identifier.
	CredentialID string
	// Revision is the Controller revision used for authentication.
	Revision uint64
	// OwnerNodeID is the configured execution owner.
	OwnerNodeID uint64
	// DigestSHA256 is the non-reversible digest forwarded instead of the raw token.
	DigestSHA256 string
}

// Verifier authenticates opaque MCP credentials against current desired state.
type Verifier struct {
	state       StateReader
	localNodeID uint64
}

// NewVerifier creates a Controller-state-backed verifier.
func NewVerifier(state StateReader) *Verifier {
	return &Verifier{state: state}
}

// NewNodeVerifier creates a verifier that also enforces owner-local forwarded execution.
func NewNodeVerifier(state StateReader, localNodeID uint64) *Verifier {
	return &Verifier{state: state, localNodeID: localNodeID}
}

// VerifyBearer validates one complete opaque bearer token in constant time.
func (v *Verifier) VerifyBearer(ctx context.Context, raw string) (Principal, error) {
	if v == nil || v.state == nil {
		return Principal{}, ErrOwnerUnavailable
	}
	state, err := v.state.OpsMCPDesiredState(ctx)
	if err != nil {
		return Principal{}, ErrOwnerUnavailable
	}
	if !state.Enabled {
		return Principal{}, ErrDisabled
	}
	credentialID, ok := parseToken(raw)
	if !ok {
		return Principal{}, ErrUnauthorized
	}
	sum := sha256.Sum256([]byte(raw))
	for _, credential := range state.Credentials {
		if credential.ID != credentialID {
			continue
		}
		expected, err := hex.DecodeString(credential.DigestSHA256)
		if err != nil || len(expected) != sha256.Size || subtle.ConstantTimeCompare(sum[:], expected) != 1 {
			return Principal{}, ErrUnauthorized
		}
		return Principal{
			CredentialID: credentialID, Revision: state.Revision, OwnerNodeID: state.OwnerNodeID,
			DigestSHA256: hex.EncodeToString(sum[:]),
		}, nil
	}
	return Principal{}, ErrUnauthorized
}

// VerifyForward revalidates a digest and revision on the execution owner.
func (v *Verifier) VerifyForward(ctx context.Context, credentialID, digestSHA256 string, expectedRevision uint64) (Principal, error) {
	if v == nil || v.state == nil {
		return Principal{}, ErrOwnerUnavailable
	}
	state, err := v.state.OpsMCPDesiredState(ctx)
	if err != nil {
		return Principal{}, ErrOwnerUnavailable
	}
	if !state.Enabled {
		return Principal{}, ErrDisabled
	}
	if state.Revision != expectedRevision {
		return Principal{}, ErrStateChanged
	}
	if v.localNodeID != 0 && state.OwnerNodeID != v.localNodeID {
		return Principal{}, ErrOwnerUnavailable
	}
	actual, err := hex.DecodeString(strings.TrimSpace(digestSHA256))
	if err != nil || len(actual) != sha256.Size || !validCredentialID(credentialID) {
		return Principal{}, ErrUnauthorized
	}
	for _, credential := range state.Credentials {
		if credential.ID != credentialID {
			continue
		}
		expected, err := hex.DecodeString(credential.DigestSHA256)
		if err != nil || len(expected) != sha256.Size || subtle.ConstantTimeCompare(actual, expected) != 1 {
			return Principal{}, ErrUnauthorized
		}
		return Principal{
			CredentialID: credentialID, Revision: state.Revision, OwnerNodeID: state.OwnerNodeID,
			DigestSHA256: hex.EncodeToString(actual),
		}, nil
	}
	return Principal{}, ErrUnauthorized
}

func parseToken(raw string) (string, bool) {
	if len(raw) < len("wko_a_b") || !strings.HasPrefix(raw, "wko_") || strings.TrimSpace(raw) != raw {
		return "", false
	}
	rest := strings.TrimPrefix(raw, "wko_")
	secretLength := base64.RawURLEncoding.EncodedLen(32)
	separator := len(rest) - secretLength - 1
	if separator < 1 || separator >= len(rest) || rest[separator] != '_' {
		return "", false
	}
	credentialID := rest[:separator]
	if !validCredentialID(credentialID) {
		return "", false
	}
	secret, err := base64.RawURLEncoding.DecodeString(rest[separator+1:])
	if err != nil || len(secret) != 32 {
		return "", false
	}
	return credentialID, true
}

func validCredentialID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

// IsAuthenticationFailure reports whether err must produce HTTP 401.
func IsAuthenticationFailure(err error) bool {
	return errors.Is(err, ErrUnauthorized)
}
