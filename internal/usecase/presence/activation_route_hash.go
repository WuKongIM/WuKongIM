package presence

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// ActivationRouteHash joins owner and authority failure logs without raw identity.
// A zero identity is unknown; the digest never changes business route matching.
func ActivationRouteHash(ownerNodeID, ownerBootID, sessionID uint64) [16]byte {
	if ownerNodeID == 0 || ownerBootID == 0 || sessionID == 0 {
		return [16]byte{}
	}
	var tuple [24]byte
	binary.BigEndian.PutUint64(tuple[0:8], ownerNodeID)
	binary.BigEndian.PutUint64(tuple[8:16], ownerBootID)
	binary.BigEndian.PutUint64(tuple[16:24], sessionID)
	h := sha256.New()
	_, _ = h.Write([]byte("wukongim/presence-route/v1\x00"))
	_, _ = h.Write(tuple[:])
	var digest [16]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

type activationRouteError struct {
	err  error
	hash [16]byte
}

func (e *activationRouteError) Error() string                 { return e.err.Error() }
func (e *activationRouteError) Unwrap() error                 { return e.err }
func (e *activationRouteError) ActivationRouteHash() [16]byte { return e.hash }

// ActivationFailureRouteHash returns only the immutable original activation route.
func ActivationFailureRouteHash(err error) ([16]byte, bool) {
	var metadata interface{ ActivationRouteHash() [16]byte }
	if !errors.As(err, &metadata) {
		return [16]byte{}, false
	}
	hash := metadata.ActivationRouteHash()
	return hash, hash != [16]byte{}
}
