package migrationv2_test

import (
	"encoding/binary"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
	"testing"
)

// compatibleMessageFixture is a synthetic derivative, NOT a new original-v2
// acceptance oracle. Only test copies have optional message flags/fields zeroed
// to exercise native installation paths independently of known source gaps.
// Original fixtures and production/source decoding remain untouched.
func compatibleMessageFixture(t *testing.T) string {
	t.Helper()
	source := unpackNamedFixture(t, "original-v2-server.tar.gz")
	clearFixtureMessageExtensions(t, source)
	return source
}

func clearFixtureMessageExtensions(t *testing.T, source string) {
	t.Helper()
	rewriteOriginalIndexFixture(t, source, func(key, value []byte, b *pebble.Batch) bool {
		if len(key) != 22 || binary.BigEndian.Uint16(key[:2]) != 0x0101 || key[2] != 1 {
			return false
		}
		var replacement []byte
		switch binary.BigEndian.Uint16(key[20:]) {
		case 0x0101, 0x0102:
			replacement = []byte{0} // header and settings
		case 0x0103:
			replacement = []byte{0, 0, 0, 0} // expiration
		case 0x010e, 0x010a:
			replacement = []byte{} // stream no and topic
		default:
			return false
		}
		require.NoError(t, b.Set(key, replacement, nil))
		return true
	})
}

// compatibleExpiryMessageFixture changes only a private synthetic fixture.
// The original v2 lifetime column is retained through all target operations.
func compatibleExpiryMessageFixture(t *testing.T, expire uint32) string {
	source := compatibleMessageFixture(t)
	if expire == 0 {
		return source
	}
	rewriteOriginalIndexFixture(t, source, func(key, value []byte, b *pebble.Batch) bool {
		if len(key) != 22 || binary.BigEndian.Uint16(key) != 0x0101 || key[2] != 1 || binary.BigEndian.Uint16(key[20:]) != 0x0103 {
			return false
		}
		encoded := make([]byte, 4)
		binary.BigEndian.PutUint32(encoded, expire)
		require.NoError(t, b.Set(key, encoded, nil))
		return true
	})
	return source
}
