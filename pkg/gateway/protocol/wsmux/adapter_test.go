package wsmux_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/protocol"
	adapterpkg "github.com/WuKongIM/WuKongIM/pkg/gateway/protocol/wsmux"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/testkit"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
)

func TestAdapterOwnsDecodedFrames(t *testing.T) {
	owner, ok := any(adapterpkg.New()).(protocol.DecodedFrameOwner)
	if !ok {
		t.Fatal("wsmux adapter does not implement DecodedFrameOwner")
	}
	if !owner.OwnsDecodedFrames() {
		t.Fatal("wsmux adapter should mark decoded frames as owned when all nested adapters own them")
	}
}

func TestAdapterDelegatesConnectAuthenticationPolicyAfterProtocolSelection(t *testing.T) {
	policy, ok := any(adapterpkg.New()).(protocol.ConnectAuthenticationPolicy)
	if !ok {
		t.Fatal("wsmux adapter does not expose its selected protocol CONNECT authentication policy")
	}

	sess := testkit.NewProtocolSession()
	required, resolved := policy.ConnectAuthenticationRequired(sess)
	if resolved || required {
		t.Fatalf("unselected policy = (%v, %v), want (false, false)", required, resolved)
	}

	for _, selected := range []string{"jsonrpc", "wkproto"} {
		sess.SetValue(gatewaytypes.SessionValueProtocolName, selected)
		required, resolved = policy.ConnectAuthenticationRequired(sess)
		if !resolved || !required {
			t.Fatalf("selected %s policy = (%v, %v), want (true, true)", selected, required, resolved)
		}
	}
}
