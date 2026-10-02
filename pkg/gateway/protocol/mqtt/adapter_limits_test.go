package mqtt_test

import (
	adapter "github.com/WuKongIM/WuKongIM/pkg/gateway/protocol/mqtt"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestIndependentOutboundLimitsPreserveInboundAndPeerBounds(t *testing.T) {
	a := adapter.NewWithOutboundLimits(wire.Limits{MaxProperties: 1, MaxPropertyBytes: 32}, wire.Limits{MaxProperties: 3, MaxPropertyBytes: 128, MaxPacketBytes: 256})
	p := &wire.Publish{Topic: "test", Properties: []wire.Property{{ID: wire.UserProperty, Text: "a", Value: "first"}, {ID: wire.UserProperty, Text: "b", Value: "second"}}}
	encoded, err := a.EncodePacket(nil, p, session.OutboundMeta{})
	require.NoError(t, err)
	_, _, err = a.DecodePackets(nil, encoded)
	require.Error(t, err)
	_, err = adapter.New(wire.Limits{MaxProperties: 1}).EncodePacket(nil, p, session.OutboundMeta{})
	require.Error(t, err)
	s := session.New(session.Config{ID: 1})
	s.SetValue(adapter.SessionMaximumPacketSize, uint32(10))
	_, err = a.EncodePacket(s, p, session.OutboundMeta{})
	require.Error(t, err)
	p.Properties = append(p.Properties, p.Properties...)
	_, err = a.EncodePacket(nil, p, session.OutboundMeta{})
	require.Error(t, err)
}
