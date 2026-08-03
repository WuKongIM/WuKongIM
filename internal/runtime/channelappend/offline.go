package channelappend

// OfflineRecipientObserverEligible reports whether an ordinary durable commit
// may emit plugin or webhook offline-recipient side effects.
func OfflineRecipientObserverEligible(event CommittedEnvelope) bool {
	return event.MessageSeq > 0 && !event.SyncOnce && len(event.MessageScopedUIDs) == 0
}
