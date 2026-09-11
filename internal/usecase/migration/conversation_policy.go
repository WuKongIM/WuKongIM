package migration

import (
	"encoding/hex"
	"errors"
	"fmt"
)

// ConversationStateRecovery binds an operator's choice of the original unique
// index record, including every physical row in the conflicting Leader group.
type ConversationStateRecovery struct {
	NodeID uint64 `json:"node_id"`
	// LogicalKey is IdentityKey(uid, channelID, channelType).
	LogicalKey string `json:"logical_key"`
	// IndexedSHA256 hashes json.Marshal(Row) for the original indexed primary.
	IndexedSHA256 string `json:"indexed_sha256"`
	// RowsSHA256 hashes each original row SHA256 followed by a newline, in
	// ascending physical-ID order. Changing any duplicate invalidates consent.
	RowsSHA256 string `json:"rows_sha256"`
}

func validateConversationRecoveryPolicy(p *MetadataPolicy) error {
	if p == nil {
		return nil
	}
	if (p.PreserveAllConversations || len(p.ConversationRecoveries) > 0) && p.ConversationLookup != "v2_active_slot" {
		return errors.New("conversation recovery requires v2_active_slot and the original list limit")
	}
	if len(p.ConversationRecoveries) > 1024 {
		return errors.New("conversation recovery exceeds bounded exact-group inventory")
	}
	seen := make(map[string]bool, len(p.ConversationRecoveries))
	for _, r := range p.ConversationRecoveries {
		key := fmt.Sprintf("%020d/%s", r.NodeID, r.LogicalKey)
		if r.NodeID == 0 || r.LogicalKey == "" || len(r.LogicalKey) > 16384 || seen[key] {
			return errors.New("invalid or duplicate conversation recovery identity")
		}
		seen[key] = true
		for _, digest := range []string{r.IndexedSHA256, r.RowsSHA256} {
			b, err := hex.DecodeString(digest)
			if err != nil || len(b) != 32 || hex.EncodeToString(b) != digest {
				return errors.New("conversation recovery requires exact lowercase SHA256 digests")
			}
		}
	}
	return nil
}

// recoverIndexedConversation accepts only the approved group's unchanged row
// evidence. It cannot authorize another group or combine fields from its rows.
func recoverIndexedConversation(p *MetadataPolicy, group, indexedSHA, rowsSHA string) (bool, error) {
	for _, r := range p.ConversationRecoveries {
		if group != fmt.Sprintf("%020d/%s", r.NodeID, r.LogicalKey) {
			continue
		}
		if r.IndexedSHA256 != indexedSHA || r.RowsSHA256 != rowsSHA {
			return false, errors.New("approved conversation recovery differs from original row evidence")
		}
		return true, nil
	}
	return false, nil
}
