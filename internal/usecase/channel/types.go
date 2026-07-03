package channel

import (
	"context"

	channelmembers "github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
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
	// Large marks a large-group channel.
	Large bool
	// Ban blocks channel messaging when true.
	Ban bool
	// Disband marks a channel as disbanded; currently accepted for compatibility.
	Disband bool
	// SendBan blocks sending while allowing receives; currently accepted for compatibility.
	SendBan bool
	// AllowStranger permits stranger sends to person channels when personal whitelist enforcement is enabled.
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

// SubscriberMutationEvent reports a successful ordinary subscriber-list mutation.
type SubscriberMutationEvent struct {
	ChannelKey
	// Large reports whether the channel should use large-group fanout after the mutation.
	Large bool
	// SubscriberMutationVersion is the durable subscriber-list version after the mutation.
	SubscriberMutationVersion uint64
	// Reset reports that AddedUIDs replaces the full ordinary subscriber snapshot.
	Reset bool
	// AddedUIDs are ordinary subscribers appended by the mutation.
	AddedUIDs []string
	// RemovedUIDs are ordinary subscribers removed by the mutation.
	RemovedUIDs []string
}

// SubscriberMutationObserver receives successful ordinary subscriber-list mutations.
type SubscriberMutationObserver interface {
	// ObserveSubscriberMutation observes a committed ordinary subscriber-list mutation.
	ObserveSubscriberMutation(context.Context, SubscriberMutationEvent)
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

// MemberListPageRequest configures one paginated channel member-list read.
type MemberListPageRequest struct {
	ChannelKey
	// AfterUID resumes the page after this UID.
	AfterUID string
	// Limit bounds the number of members returned.
	Limit int
}

// MemberListPageResult contains one member-list page.
type MemberListPageResult struct {
	// Members is the current page of channel members.
	Members []Member
	// NextCursor is the UID cursor returned by storage.
	NextCursor string
	// HasMore reports whether another page is available.
	HasMore bool
}

func namespacedListChannelID(kind memberListKind, key ChannelKey) string {
	contractKey := channelmembers.ChannelKey{ChannelID: key.ChannelID, ChannelType: key.ChannelType}
	switch kind {
	case allowListKind:
		return channelmembers.AllowlistChannelID(contractKey)
	case denyListKind:
		return channelmembers.DenylistChannelID(contractKey)
	case tempListKind:
		return channelmembers.TempListChannelID(key.ChannelID)
	default:
		return ""
	}
}
