package message

import (
	"strconv"
	"strings"
)

const legacyReadKeyPrefix = "wk3-legacy-"

// LegacyReadClientMsgNo supplies a stable read identity for old committed records
// that lack a client number. It does not change stored data or create a SEND
// idempotency key. Existing nonempty values remain byte-for-byte unchanged.
func LegacyReadClientMsgNo(messageID uint64, original string) string {
	if original != "" || messageID == 0 {
		return original
	}
	return legacyReadKeyPrefix + strconv.FormatUint(messageID, 10)
}

func legacyReadMessageID(key string) (uint64, bool) {
	if !strings.HasPrefix(key, legacyReadKeyPrefix) {
		return 0, false
	}
	value := strings.TrimPrefix(key, legacyReadKeyPrefix)
	id, err := strconv.ParseUint(value, 10, 64)
	return id, err == nil && id > 0 && strconv.FormatUint(id, 10) == value
}
