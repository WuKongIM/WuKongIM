package session

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var benchmarkValueSink any
var benchmarkSessionSink Session
var benchmarkWriteErr error

func BenchmarkSessionNewIdle(b *testing.B) {
	for i := 0; i < b.N; i++ {
		benchmarkSessionSink = newSession(1, "listener-a", "remote-a", "local-a", nil)
	}
}

func BenchmarkSessionWriteFrame(b *testing.B) {
	sess := newSession(1, "listener-a", "remote-a", "local-a", func(frame.Frame, OutboundMeta) error { return nil })
	frameToWrite := &frame.PingPacket{}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchmarkWriteErr = sess.WriteFrame(frameToWrite)
	}
}

func BenchmarkSessionValue(b *testing.B) {
	sess := newSession(1, "listener-a", "remote-a", "local-a", nil)
	sess.SetValue("gateway.uid", "u1")
	sess.SetValue("custom.key", "custom-value")

	for _, tc := range []struct {
		name string
		key  string
	}{
		{name: "hot_uid", key: "gateway.uid"},
		{name: "custom", key: "custom.key"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchmarkValueSink = sess.Value(tc.key)
			}
		})
	}
}
