package frame

import (
	"crypto/sha256"
	"encoding/binary"
)

// HandshakeFingerprint identifies the actual CONNECT public-key/timestamp tuple
// without retaining identities or credentials. Empty keys mean unobserved input.
// Repeated tuples have the same digest; this is not a globally unique attempt ID.
func HandshakeFingerprint(clientKey string, clientTimestamp int64) [16]byte {
	var result [16]byte
	if clientKey == "" {
		return result
	}
	h := sha256.New()
	_, _ = h.Write([]byte("wukongim/connect-attempt/v1\x00"))
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(clientTimestamp))
	_, _ = h.Write(stamp[:])
	_, _ = h.Write([]byte(clientKey))
	copy(result[:], h.Sum(nil))
	return result
}
