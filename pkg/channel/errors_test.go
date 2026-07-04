package channel

import (
	"errors"
	"testing"
)

func TestErrorSentinelsUsePromotedPrefix(t *testing.T) {
	if got, want := ErrNotReady.Error(), "channel: not ready"; got != want {
		t.Fatalf("ErrNotReady.Error() = %q, want %q", got, want)
	}
	if got, want := ErrStaleMeta.Error(), "channel: stale meta"; got != want {
		t.Fatalf("ErrStaleMeta.Error() = %q, want %q", got, want)
	}
}

func TestErrorMatchesAcceptsLegacyChannelV2Text(t *testing.T) {
	if !ErrorMessageMatches("nodetransport: remote error: channelv2: not ready", ErrNotReady) {
		t.Fatal("ErrorMessageMatches() did not accept legacy not-ready text")
	}
	if !ErrorMatches(errors.New("remote: channelv2: stale meta"), ErrStaleMeta) {
		t.Fatal("ErrorMatches() did not accept legacy stale-meta text")
	}
	if ErrorMessageMatches("nodetransport: remote error: channelv2: not ready", ErrNotLeader) {
		t.Fatal("ErrorMessageMatches() matched the wrong sentinel")
	}
}

func TestTrimErrorMessagePrefixAcceptsPromotedAndLegacyText(t *testing.T) {
	if got, want := TrimErrorMessagePrefix("channel: not ready: follower is loading", ErrNotReady), "follower is loading"; got != want {
		t.Fatalf("TrimErrorMessagePrefix(promoted) = %q, want %q", got, want)
	}
	if got, want := TrimErrorMessagePrefix("channelv2: not ready: follower is loading", ErrNotReady), "follower is loading"; got != want {
		t.Fatalf("TrimErrorMessagePrefix(legacy) = %q, want %q", got, want)
	}
	if got := TrimErrorMessagePrefix("channelv2: not ready", ErrNotReady); got != "" {
		t.Fatalf("TrimErrorMessagePrefix(exact legacy) = %q, want empty", got)
	}
}
