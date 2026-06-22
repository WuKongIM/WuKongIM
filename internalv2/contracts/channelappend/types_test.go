package channelappend_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelappend"
)

func TestSendCommandCloneClonesSlices(t *testing.T) {
	cmd := channelappend.SendCommand{
		Payload:           []byte("hello"),
		MessageScopedUIDs: []string{"u1", "u2"},
		DeviceFlag:        3,
		Origin:            channelappend.SendOriginPlugin,
		HookDepth:         1,
		SkipPluginHooks:   true,
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
	if cloned.DeviceFlag != 3 || cloned.Origin != channelappend.SendOriginPlugin || cloned.HookDepth != 1 || !cloned.SkipPluginHooks {
		t.Fatalf("hook/device controls were not preserved: %#v", cloned)
	}
}

func TestAppendBatchRequestClonePreservesAuthorityFence(t *testing.T) {
	req := channelappend.AppendBatchRequest{
		ChannelID:           channelappend.ChannelID{ID: "room", Type: 2},
		ExpectedEpoch:       11,
		ExpectedLeaderEpoch: 22,
		Messages: []channelappend.Message{{
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
