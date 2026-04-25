package gateway_test

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/binding"
	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func TestGatewayLoggerFlowsToTransportConnectionLogs(t *testing.T) {
	logger := newGatewayRecordingLogger()
	handler := testkit.NewRecordingHandler()
	gw, err := gateway.New(gateway.Options{
		Handler: handler,
		Logger:  logger,
		Listeners: []gateway.ListenerOptions{
			binding.TCPWKProto("tcp-wkproto", "127.0.0.1:0"),
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = gw.Stop() })

	conn, err := net.Dial("tcp", gw.ListenerAddr("tcp-wkproto"))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.Close()

	waitUntil(t, time.Second, func() bool {
		return logger.hasEvent("gateway.transport.conn.connected")
	})
}

type gatewayRecordingLogger struct {
	mu      sync.Mutex
	entries []gatewayRecordedEntry
}

type gatewayRecordedEntry struct {
	fields []wklog.Field
}

func newGatewayRecordingLogger() *gatewayRecordingLogger { return &gatewayRecordingLogger{} }

func (r *gatewayRecordingLogger) Debug(_ string, fields ...wklog.Field) { r.record(fields...) }
func (r *gatewayRecordingLogger) Info(_ string, fields ...wklog.Field)  { r.record(fields...) }
func (r *gatewayRecordingLogger) Warn(_ string, fields ...wklog.Field)  { r.record(fields...) }
func (r *gatewayRecordingLogger) Error(_ string, fields ...wklog.Field) { r.record(fields...) }
func (r *gatewayRecordingLogger) Fatal(_ string, fields ...wklog.Field) { r.record(fields...) }

func (r *gatewayRecordingLogger) Named(string) wklog.Logger {
	return r
}

func (r *gatewayRecordingLogger) With(...wklog.Field) wklog.Logger {
	return r
}

func (r *gatewayRecordingLogger) Sync() error { return nil }

func (r *gatewayRecordingLogger) record(fields ...wklog.Field) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, gatewayRecordedEntry{fields: append([]wklog.Field(nil), fields...)})
}

func (r *gatewayRecordingLogger) hasEvent(event string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range r.entries {
		for _, field := range entry.fields {
			if field.Key == "event" && field.Value == event {
				return true
			}
		}
	}
	return false
}
