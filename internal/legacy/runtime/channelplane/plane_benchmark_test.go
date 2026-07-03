package channelplane

import (
	"context"
	"testing"
)

func BenchmarkChannelPlaneLocalAppendHotPath(b *testing.B) {
	p, err := New(Options{
		ReactorCount: 1,
		LocalNode:    1,
		Resolver:     staticResolver{route: localRoute("bench-local")},
		LocalOwner:   noopOwner{},
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := p.Start(); err != nil {
		b.Fatal(err)
	}
	defer stopPlane(b, p)

	ctx := context.Background()
	req := appendReq("bench-local", 1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.AppendBatch(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}
