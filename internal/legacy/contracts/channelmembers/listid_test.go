package channelmembers

import "testing"

func TestAllowDenyListChannelIDsMatchLegacyNamespace(t *testing.T) {
	key := ChannelKey{ChannelID: "g1", ChannelType: 2}
	if got, want := AllowlistChannelID(key), "__wk_internal_memberlist__/allow/2/ZzE"; got != want {
		t.Fatalf("AllowlistChannelID() = %q, want %q", got, want)
	}
	if got, want := DenylistChannelID(key), "__wk_internal_memberlist__/deny/2/ZzE"; got != want {
		t.Fatalf("DenylistChannelID() = %q, want %q", got, want)
	}
}
