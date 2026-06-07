package channelmembers

import "testing"

func TestMemberListChannelIDsMatchLegacyNamespace(t *testing.T) {
	key := ChannelKey{ChannelID: "g1", ChannelType: 2}
	if got, want := AllowlistChannelID(key), "__wk_internal_memberlist__/allow/2/ZzE"; got != want {
		t.Fatalf("AllowlistChannelID() = %q, want %q", got, want)
	}
	if got, want := DenylistChannelID(key), "__wk_internal_memberlist__/deny/2/ZzE"; got != want {
		t.Fatalf("DenylistChannelID() = %q, want %q", got, want)
	}
	if got, want := TempListChannelID("tmp"), "__wk_internal_memberlist__/temp/8/dG1w"; got != want {
		t.Fatalf("TempListChannelID() = %q, want %q", got, want)
	}
}
