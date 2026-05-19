package session

import "testing"

var benchmarkValueSink any
var benchmarkSessionSink Session

func BenchmarkSessionNewIdle(b *testing.B) {
	for i := 0; i < b.N; i++ {
		benchmarkSessionSink = newSession(1, "listener-a", "remote-a", "local-a", 64, 0, nil)
	}
}

func BenchmarkSessionEnqueueDequeueEncoded(b *testing.B) {
	payload := make([]byte, 1024)
	for i := range payload {
		payload[i] = byte(i)
	}

	for _, tc := range []struct {
		name    string
		enqueue func(*session, []byte) error
	}{
		{name: "copy", enqueue: func(s *session, p []byte) error { return s.EnqueueEncoded(p) }},
		{name: "owned", enqueue: func(s *session, p []byte) error { return s.EnqueueOwnedEncoded(p) }},
	} {
		b.Run(tc.name, func(b *testing.B) {
			sess := newSession(1, "listener-a", "remote-a", "local-a", 1, 0, nil)

			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := tc.enqueue(sess, payload); err != nil {
					b.Fatalf("enqueue failed: %v", err)
				}
				if _, ok := sess.DequeueEncoded(); !ok {
					b.Fatal("dequeue failed")
				}
				sess.ReleaseEncoded(payload)
			}
		})
	}
}

func BenchmarkSessionValue(b *testing.B) {
	sess := newSession(1, "listener-a", "remote-a", "local-a", 1, 0, nil)
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
