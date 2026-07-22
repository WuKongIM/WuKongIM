package backup

import (
	"context"
	"fmt"
	"strings"
)

// keyPinnedManifestSigner restricts signing and verification to an explicit
// configured set of trusted manifest key identifiers.
type keyPinnedManifestSigner struct {
	// signer performs the provider-specific cryptographic operation.
	signer ManifestSigner
	// trustedKeyIDs is the explicit operator-configured verification allowlist.
	trustedKeyIDs map[string]struct{}
}

// NewKeyPinnedManifestSigner binds manifest trust to configured signing keys.
// Multiple keys allow an operator-controlled rotation window.
func NewKeyPinnedManifestSigner(signer ManifestSigner, trustedKeyIDs ...string) (ManifestSigner, error) {
	if signer == nil || len(trustedKeyIDs) == 0 {
		return nil, fmt.Errorf("backup manifest signer: signer and trusted key ids are required")
	}
	trusted := make(map[string]struct{}, len(trustedKeyIDs))
	for _, keyID := range trustedKeyIDs {
		keyID = strings.TrimSpace(keyID)
		if keyID == "" {
			return nil, fmt.Errorf("backup manifest signer: trusted key id is required")
		}
		trusted[keyID] = struct{}{}
	}
	return &keyPinnedManifestSigner{signer: signer, trustedKeyIDs: trusted}, nil
}

func (s *keyPinnedManifestSigner) Sign(ctx context.Context, keyID string, message []byte) (ManifestSignature, error) {
	keyID = strings.TrimSpace(keyID)
	if !s.trusts(keyID) {
		return ManifestSignature{}, fmt.Errorf("%w: signing key %q is not trusted", ErrInvalidSignature, keyID)
	}
	return s.signer.Sign(ctx, keyID, message)
}

func (s *keyPinnedManifestSigner) Verify(ctx context.Context, signature ManifestSignature, message []byte) error {
	if !s.trusts(strings.TrimSpace(signature.KeyID)) {
		return fmt.Errorf("%w: signing key %q is not trusted", ErrInvalidSignature, signature.KeyID)
	}
	return s.signer.Verify(ctx, signature, message)
}

func (s *keyPinnedManifestSigner) trusts(keyID string) bool {
	if s == nil || s.signer == nil || keyID == "" {
		return false
	}
	_, ok := s.trustedKeyIDs[keyID]
	return ok
}

var _ ManifestSigner = (*keyPinnedManifestSigner)(nil)
