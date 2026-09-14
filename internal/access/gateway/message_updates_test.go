package gateway

import (
	"context"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"testing"
)

type capabilitiesStub struct {
	uid     string
	session uint64
	enabled bool
	calls   int
}

func (c *capabilitiesStub) EnableMessageUpdates(uid string, id uint64, enabled bool) error {
	c.uid = uid
	c.session = id
	c.enabled = enabled
	c.calls++
	return nil
}
func TestMessageUpdateCapabilityIsExplicitAndAuthenticated(t *testing.T) {
	caps := &capabilitiesStub{}
	var writes []frame.Frame
	sess := session.New(session.Config{ID: 10, WriteFrameFn: func(f frame.Frame, _ session.OutboundMeta) error { writes = append(writes, f); return nil }})
	handler := New(Options{MessageUpdateCapabilities: caps})
	ctx := coregateway.Context{Session: sess, RequestContext: context.Background()}
	packet := &frame.EventPacket{Type: "message_updates.enable", Data: []byte(`{"enabled":true}`)}
	if err := handler.OnFrame(ctx, packet); err == nil || caps.calls != 0 {
		t.Fatal("unauthenticated negotiation accepted")
	}
	sess.SetValue(coregateway.SessionValueUID, "alice")
	if err := handler.OnFrame(ctx, packet); err != nil {
		t.Fatal(err)
	}
	if caps.uid != "alice" || caps.session != 10 || !caps.enabled || len(writes) != 1 || writes[0].(*frame.EventPacket).Type != "message_updates.ready" {
		t.Fatalf("capability=%+v writes=%v", caps, writes)
	}
	packet.Data = []byte(`{}`)
	if err := handler.OnFrame(ctx, packet); err == nil || caps.calls != 1 {
		t.Fatal("missing enable state accepted")
	}
}
