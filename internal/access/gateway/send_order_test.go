package gateway

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestDeferredSendOrderKeyPreservesProtocolBoundaryAndAvoidsAdmissionAllocation(t *testing.T) {
	keyer := New(Options{}).WithChannelOrderedDeferredSends(message.New(message.Options{})).(coregateway.SendOrderKeyer)
	sess := session.New(session.Config{ID: 1})
	sess.SetValue(coregateway.SessionValueUID, "sender")
	sess.SetValue(gatewaytypes.SessionValueProtocolName, "wkproto")
	person := uint8(frame.ChannelTypePerson)
	key := keyer.SendOrderKey(sess, "peer", person)
	require.NotEmpty(t, key.ID)
	for _, target := range []string{"peer", "peer@sender", "sender@peer"} {
		t.Run(target, func(t *testing.T) {
			require.Equal(t, key, keyer.SendOrderKey(sess, target, person))
			var got coregateway.SendOrderKey
			allocations := testing.AllocsPerRun(1000, func() {
				got = keyer.SendOrderKey(sess, target, person)
			})
			require.Equal(t, key, got)
			require.Zero(t, allocations, "deriving the order key must not allocate before shard admission")
		})
	}
	require.NotEqual(t, key, keyer.SendOrderKey(sess, "another-peer", person))
	for _, invalid := range []string{"", "missing@sender@peer", "wrong@peer", "sender@"} {
		require.Empty(t, keyer.SendOrderKey(sess, invalid, person).ID)
	}
	for _, protocol := range []string{"jsonrpc", "mqtt", "wsmux", ""} {
		sess.SetValue(gatewaytypes.SessionValueProtocolName, protocol)
		require.Empty(t, keyer.SendOrderKey(sess, "peer", person).ID, "protocol %q retains connection order", protocol)
	}
}
