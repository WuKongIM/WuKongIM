package delivery

// SubscriberSourceKind describes how a subscriber snapshot is produced.
type SubscriberSourceKind string

const (
	// SubscriberSourceKindDerived covers channels whose subscriber set is derived from the channel ID.
	SubscriberSourceKindDerived SubscriberSourceKind = "derived"
	// SubscriberSourceKindPagedStore covers ordinary store-backed channels that page subscribers directly.
	SubscriberSourceKindPagedStore SubscriberSourceKind = "paged_store"
	// SubscriberSourceKindOverlayStore covers store-backed channels that merge an overlay on top of paging.
	SubscriberSourceKindOverlayStore SubscriberSourceKind = "overlay_store"
	// SubscriberSourceKindMessageScoped covers one-shot request-scoped subscriber sets.
	SubscriberSourceKindMessageScoped SubscriberSourceKind = "message_scoped"
)

// SubscriberSource describes the durable and cacheability fence for one subscriber snapshot.
type SubscriberSource struct {
	// Kind identifies the resolution strategy used for the snapshot.
	Kind SubscriberSourceKind
	// ChannelID is the delivery channel requested by the caller.
	ChannelID string
	// ChannelType is the delivery channel type requested by the caller.
	ChannelType uint8
	// SourceChannelID is the underlying channel whose subscriber mutation version fences reuse.
	SourceChannelID string
	// SourceChannelType is the underlying source channel type used for staleness checks.
	SourceChannelType uint8
	// SubscriberMutationVersion is the durable version for the channel currently being resolved.
	SubscriberMutationVersion uint64
	// SourceSubscriberMutationVersion is the durable version of the underlying source channel.
	SourceSubscriberMutationVersion uint64
	// ReusableTagState reports whether the snapshot may back a reusable channel-level tag.
	ReusableTagState bool
}

// SubscriberSnapshotRequest carries request-scoped subscribers for one-shot temp delivery.
type SubscriberSnapshotRequest struct {
	// MessageScopedUIDs contains the exact UIDs that should be used only for this request.
	MessageScopedUIDs []string
}

