package channelwrite_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"
)

func TestSendCommandCloneClonesSlices(t *testing.T) {
	cmd := channelwrite.SendCommand{
		Payload:           []byte("hello"),
		MessageScopedUIDs: []string{"u1", "u2"},
	}
	cloned := cmd.Clone()
	cmd.Payload[0] = 'x'
	cmd.MessageScopedUIDs[0] = "changed"
	if string(cloned.Payload) != "hello" {
		t.Fatalf("payload clone mutated: %q", cloned.Payload)
	}
	if got := cloned.MessageScopedUIDs[0]; got != "u1" {
		t.Fatalf("scoped uid clone mutated: %q", got)
	}
}

func TestAppendBatchRequestClonePreservesAuthorityFence(t *testing.T) {
	req := channelwrite.AppendBatchRequest{
		ChannelID:           channelwrite.ChannelID{ID: "room", Type: 2},
		ExpectedEpoch:       11,
		ExpectedLeaderEpoch: 22,
		Messages: []channelwrite.Message{{
			MessageID: 1,
			Payload:   []byte("hello"),
		}},
	}

	cloned := req.Clone()
	req.Messages[0].Payload[0] = 'x'

	if cloned.ExpectedEpoch != 11 {
		t.Fatalf("ExpectedEpoch = %d, want 11", cloned.ExpectedEpoch)
	}
	if cloned.ExpectedLeaderEpoch != 22 {
		t.Fatalf("ExpectedLeaderEpoch = %d, want 22", cloned.ExpectedLeaderEpoch)
	}
	if string(cloned.Messages[0].Payload) != "hello" {
		t.Fatalf("payload clone mutated: %q", cloned.Messages[0].Payload)
	}
}
