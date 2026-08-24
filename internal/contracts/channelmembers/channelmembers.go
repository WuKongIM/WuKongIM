// Package channelmembers defines the dependency-light, legacy-compatible
// namespace used for internal allowlist, denylist, and temporary member rows.
// It must not import access, app, gateway, cluster, or storage packages.
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

// AllowlistChannelID returns the stable legacy-compatible channel ID for allowlist members.
func AllowlistChannelID(key ChannelKey) string {
	return namespacedListChannelID("allow", key)
}

// DenylistChannelID returns the stable legacy-compatible channel ID for denylist members.
func DenylistChannelID(key ChannelKey) string {
	return namespacedListChannelID("deny", key)
}

// TempListChannelID returns the stable legacy-compatible channel ID for temporary members.
func TempListChannelID(channelID string) string {
	return namespacedListChannelID("temp", ChannelKey{ChannelID: channelID, ChannelType: 8})
}

func namespacedListChannelID(kind string, key ChannelKey) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(key.ChannelID))
	return fmt.Sprintf("__wk_internal_memberlist__/%s/%d/%s", kind, key.ChannelType, encoded)
}
