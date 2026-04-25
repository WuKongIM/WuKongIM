package presence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func BenchmarkHeartbeatBuckets100kRoutes(b *testing.B) {
	const (
		totalRoutes = 100000
		groupCount  = 8
	)

	onlineReg := online.NewRegistry()
	for i := 0; i < totalRoutes; i++ {
		slotID := uint64(i%groupCount + 1)
		_ = onlineReg.Register(online.OnlineConn{
			SessionID:   uint64(i + 1),
			UID:         fmt.Sprintf("u-%d", i),
			DeviceID:    fmt.Sprintf("d-%d", i),
			DeviceFlag:  frame.APP,
			DeviceLevel: frame.DeviceLevelMaster,
			SlotID:      slotID,
			State:       online.LocalRouteStateActive,
			Listener:    "tcp",
			ConnectedAt: time.Unix(200, 0),
			Session:     session.New(session.Config{ID: uint64(i + 1), Listener: "tcp"}),
		})
	}

	authority := &fakeAuthorityClient{}
	app := New(Options{
		LocalNodeID:     1,
		GatewayBootID:   101,
		Online:          onlineReg,
		AuthorityClient: authority,
		Now:             func() time.Time { return time.Unix(200, 0) },
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		authority.heartbeatCalls = authority.heartbeatCalls[:0]
		authority.replayCalls = authority.replayCalls[:0]
		if err := app.HeartbeatOnce(context.Background()); err != nil {
			b.Fatal(err)
		}
		if len(authority.heartbeatCalls) != groupCount {
			b.Fatalf("expected %d heartbeats, got %d", groupCount, len(authority.heartbeatCalls))
		}
	}
}
