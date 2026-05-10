package channel

import (
	"encoding/base64"
	"fmt"
)

const tempChannelType uint8 = 8

type memberListKind string

const (
	allowListKind memberListKind = "allow"
	denyListKind  memberListKind = "deny"
	tempListKind  memberListKind = "temp"
)

// ChannelKey identifies a channel for metadata and member-list operations.
type ChannelKey struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType uint8
}

// Info carries legacy channel metadata accepted by the HTTP API.
type Info struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType uint8
	// Large marks a large-group channel; currently accepted for compatibility.
	Large bool
	// Ban blocks channel messaging when true.
	Ban bool
	// Disband marks a channel as disbanded; currently accepted for compatibility.
	Disband bool
	// SendBan blocks sending while allowing receives; currently accepted for compatibility.
	SendBan bool
	// AllowStranger permits stranger messages for person channels; currently accepted for compatibility.
	AllowStranger bool
}

// UpsertCommand updates channel metadata and optionally applies subscribers.
type UpsertCommand struct {
	// Info contains the metadata to persist.
	Info Info
	// Reset replaces the current subscriber snapshot before appending Subscribers.
	Reset bool
	// Subscribers contains legacy subscriber UIDs.
	Subscribers []string
}

// SubscriberCommand changes ordinary channel subscribers.
type SubscriberCommand struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType uint8
	// Reset replaces the current subscriber snapshot before appending Subscribers.
	Reset bool
	// Subscribers contains legacy subscriber UIDs.
	Subscribers []string
}

// TempSubscriberCommand replaces temporary subscribers for a temporary channel.
type TempSubscriberCommand struct {
	// ChannelID is the temporary channel identifier.
	ChannelID string
	// UIDs contains temporary subscriber UIDs.
	UIDs []string
}

// MemberCommand changes allowlist or denylist members.
type MemberCommand struct {
	ChannelKey
	// UIDs contains allowlist or denylist member UIDs.
	UIDs []string
}

// Member is the legacy list-member response item.
type Member struct {
	// ID is preserved for the legacy response shape.
	ID uint64 `json:"id"`
	// UID is the member user identifier.
	UID string `json:"uid"`
}

// MemberListResult contains legacy channel members.
type MemberListResult struct {
	// Members is the legacy member array returned by whitelist queries.
	Members []Member
}

func namespacedListChannelID(kind memberListKind, key ChannelKey) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(key.ChannelID))
	return fmt.Sprintf("__wk_internal_memberlist__/%s/%d/%s", kind, key.ChannelType, encoded)
}
