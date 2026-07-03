package channelmembers

import (
	"encoding/base64"
	"fmt"
)

// ChannelKey identifies the logical channel whose member-list namespace is derived.
type ChannelKey struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType uint8
}

// AllowlistChannelID returns the internal subscriber-list channel ID for allowlist members.
func AllowlistChannelID(key ChannelKey) string {
	return namespacedListChannelID("allow", key)
}

// DenylistChannelID returns the internal subscriber-list channel ID for denylist members.
func DenylistChannelID(key ChannelKey) string {
	return namespacedListChannelID("deny", key)
}

// TempListChannelID returns the internal subscriber-list channel ID for temporary members.
func TempListChannelID(channelID string) string {
	return namespacedListChannelID("temp", ChannelKey{ChannelID: channelID, ChannelType: 8})
}

func namespacedListChannelID(kind string, key ChannelKey) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(key.ChannelID))
	return fmt.Sprintf("__wk_internal_memberlist__/%s/%d/%s", kind, key.ChannelType, encoded)
}
