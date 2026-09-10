package migration

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestConversationRecoveryPolicyRequiresBoundedExactEvidence(t *testing.T) {
	valid := ConversationStateRecovery{NodeID: 1, LogicalKey: IdentityKey("user", "room", uint8(2)), IndexedSHA256: strings.Repeat("a", 64), RowsSHA256: strings.Repeat("b", 64)}
	for _, mode := range []string{"valid", "missing_lookup", "zero_node", "empty_key", "short_hash", "uppercase_hash", "duplicate", "too_many"} {
		t.Run(mode, func(t *testing.T) {
			p := &MetadataPolicy{DeviceLookup: "v2_cold_start", ConversationLookup: "v2_active_slot", ConversationListLimit: 1000, PreserveAllConversations: true, ConversationRecoveries: []ConversationStateRecovery{valid}}
			switch mode {
			case "missing_lookup":
				p.ConversationLookup = ""
				p.ConversationListLimit = 0
			case "zero_node":
				p.ConversationRecoveries[0].NodeID = 0
			case "empty_key":
				p.ConversationRecoveries[0].LogicalKey = ""
			case "short_hash":
				p.ConversationRecoveries[0].RowsSHA256 = "aa"
			case "uppercase_hash":
				p.ConversationRecoveries[0].IndexedSHA256 = strings.Repeat("A", 64)
			case "duplicate":
				p.ConversationRecoveries = append(p.ConversationRecoveries, valid)
			case "too_many":
				p.ConversationRecoveries = make([]ConversationStateRecovery, 1025)
			}
			err := validateMetadataPolicy(p)
			if mode == "valid" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
