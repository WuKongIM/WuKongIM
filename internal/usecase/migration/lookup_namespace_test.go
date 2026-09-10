package migration

import (
	"fmt"
	"strings"
	"testing"
)

var lookupNamespaceBenchmarkKey string

// BenchmarkMetadataLookupNamespace compares identical final keys with a
// representative exact-group policy; it does not measure complete migration.
func BenchmarkMetadataLookupNamespace(b *testing.B) {
	p := &MetadataPolicy{DeviceLookup: "v2_cold_start", ConversationLookup: "v2_active_slot", ConversationListLimit: 1000}
	for i := 0; i < 198; i++ {
		p.ConversationReplicas = append(p.ConversationReplicas, ConversationReplicaRecovery{LogicalKey: IdentityKey(fmt.Sprint(i), "room", uint8(2)), SourceNodeID: 1001, CopiesSHA256: strings.Repeat("a", 64)})
	}
	capture := strings.Repeat("b", 64)
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cached=%t", cached), func(b *testing.B) {
			base := conversationLookupBase(capture, p)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				prefix := base
				if !cached {
					prefix = conversationLookupBase(capture, p)
				}
				lookupNamespaceBenchmarkKey = fmt.Sprintf("%schosen/%020d/%s", prefix, 1001, "logical-key")
			}
		})
	}
}
