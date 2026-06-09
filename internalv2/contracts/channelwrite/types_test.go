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
