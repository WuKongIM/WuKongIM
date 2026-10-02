package app

import (
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestMQTTProtocolReservesBoundedOutboundPropertyHeadroom(t *testing.T) {
	a := newMQTTProtocol(wire.Limits{})
	p := &wire.Publish{Topic: "test"}
	for i := 0; i < 128; i++ {
		p.Properties = append(p.Properties, wire.Property{ID: wire.UserProperty, Text: "app", Value: strings.Repeat("x", 240)})
	}
	for i := 0; i < 8; i++ {
		p.Properties = append(p.Properties, wire.Property{ID: wire.UserProperty, Text: "wk.server", Value: strings.Repeat("x", 512)})
	}
	encoded, err := a.EncodePacket(nil, p, session.OutboundMeta{})
	require.NoError(t, err)
	decoded, n, err := wire.Decode(encoded, wire.Limits{MaxProperties: 136, MaxPropertyBytes: 64 << 10})
	require.NoError(t, err)
	require.Equal(t, len(encoded), n)
	require.Equal(t, p, decoded)
	_, _, err = a.DecodePackets(nil, encoded)
	require.Error(t, err)
	p.Properties = append(p.Properties, wire.Property{ID: wire.UserProperty, Text: "extra"})
	_, err = a.EncodePacket(nil, p, session.OutboundMeta{})
	require.Error(t, err)
}
