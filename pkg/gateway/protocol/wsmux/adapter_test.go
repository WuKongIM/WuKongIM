package wsmux_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/protocol"
	adapterpkg "github.com/WuKongIM/WuKongIM/pkg/gateway/protocol/wsmux"
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
