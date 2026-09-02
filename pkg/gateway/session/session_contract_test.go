package session

import (
	"errors"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestManagerRangeUsesStableSnapshotAndPermitsMutation(t *testing.T) {
	mgr := NewManager()
	mgr.Add(newTestSession(1))
	mgr.Add(newTestSession(2))

	visited := make(map[uint64]struct{}, 2)
	mgr.Range(func(sess Session) bool {
		visited[sess.ID()] = struct{}{}
		if len(visited) == 1 {
			mgr.Remove(sess.ID())
			mgr.Add(newTestSession(99))
		}
		return true
	})

	if len(visited) != 2 {
		t.Fatalf("Range() visited %d sessions, want the two-session snapshot", len(visited))
	}
	if _, ok := visited[99]; ok {
		t.Fatal("Range() visited a session added after its snapshot")
	}
	if _, ok := mgr.Get(99); !ok {
		t.Fatal("mutation from Range callback did not complete")
	}
}

func TestManagerRangeHonorsEarlyStop(t *testing.T) {
	mgr := NewManager()
	mgr.Add(newTestSession(1))
	mgr.Add(newTestSession(2))

	visits := 0
	mgr.Range(func(Session) bool {
		visits++
		return false
	})

	if visits != 1 {
		t.Fatalf("Range() callback calls = %d, want 1 after early stop", visits)
	}
}

func TestSessionLoadOrStoreValuePreservesInitializedNilHotState(t *testing.T) {
	sess := newSession(21, "listener-a", "remote-a", "local-a", nil)

	actual, loaded := sess.LoadOrStoreValue(hotSessionValueCrypto, nil)
	if loaded || actual != nil {
		t.Fatalf("first LoadOrStoreValue() = (%#v, %v), want (nil, false)", actual, loaded)
	}

	actual, loaded = sess.LoadOrStoreValue(hotSessionValueCrypto, "replacement")
	if !loaded || actual != nil {
		t.Fatalf("second LoadOrStoreValue() = (%#v, %v), want (nil, true)", actual, loaded)
	}
	if got := sess.Value(hotSessionValueCrypto); got != nil {
		t.Fatalf("Value() = %#v, want the initialized nil value", got)
	}
}

func TestSessionLoadOrStoreValueInitializesStateOnce(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "reserved hot state", key: hotSessionValueCrypto},
		{name: "extension state", key: "extension.reply_queue"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := newSession(22, "listener-a", "remote-a", "local-a", nil)
			const contenders = 32
			type result struct {
				actual any
				loaded bool
			}

			start := make(chan struct{})
			results := make(chan result, contenders)
			var ready sync.WaitGroup
			ready.Add(contenders)
			for value := 0; value < contenders; value++ {
				go func(value int) {
					ready.Done()
					<-start
					actual, loaded := sess.LoadOrStoreValue(tt.key, value)
					results <- result{actual: actual, loaded: loaded}
				}(value)
			}
			ready.Wait()
			close(start)

			var initialized any
			initializers := 0
			allResults := make([]result, 0, contenders)
			for i := 0; i < contenders; i++ {
				got := <-results
				allResults = append(allResults, got)
				if !got.loaded {
					initialized = got.actual
					initializers++
				}
			}
			if initializers != 1 {
				t.Fatalf("LoadOrStoreValue() initializers = %d, want exactly 1", initializers)
			}
			if _, ok := initialized.(int); !ok {
				t.Fatalf("LoadOrStoreValue() initialized value = %#v, want one contender's int", initialized)
			}
			for _, got := range allResults {
				if got.actual != initialized {
					t.Fatalf("LoadOrStoreValue() actual = %#v, want initialized value %#v", got.actual, initialized)
				}
			}
			if got := sess.Value(tt.key); got != initialized {
				t.Fatalf("Value() = %#v, want initialized value %#v", got, initialized)
			}
		})
	}
}

func TestSessionReservedValuesRemainIsolated(t *testing.T) {
	sess := newSession(23, "listener-a", "remote-a", "local-a", nil)
	keys := []string{
		hotSessionValueUID,
		hotSessionValueDeviceID,
		hotSessionValueDeviceFlag,
		hotSessionValueDeviceLevel,
		hotSessionValueProtocolVersion,
		hotSessionValueProtocolName,
		hotSessionValueEncryptionEnabled,
		hotSessionValueAESKey,
		hotSessionValueAESIV,
		hotSessionValueCrypto,
	}

	for i, key := range keys {
		sess.SetValue(key, i+1)
	}
	for i, key := range keys {
		if got := sess.Value(key); got != i+1 {
			t.Fatalf("Value(%q) = %#v, want %d; reserved values must not alias", key, got, i+1)
		}
	}
}

func TestSessionWriteFramePropagatesCallbackFailureAndMetadata(t *testing.T) {
	wantErr := errors.New("transport queue full")
	var gotFrame frame.Frame
	var gotMeta OutboundMeta
	sess := New(Config{
		ID: 24,
		WriteFrameFn: func(f frame.Frame, meta OutboundMeta) error {
			gotFrame = f
			gotMeta = meta
			return wantErr
		},
	})
	wantFrame := &frame.PingPacket{}

	err := sess.WriteFrame(wantFrame, nil, WithReplyToken("reply-24"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("WriteFrame() error = %v, want %v", err, wantErr)
	}
	if gotFrame != wantFrame {
		t.Fatalf("WriteFrame() callback frame = %#v, want %#v", gotFrame, wantFrame)
	}
	if gotMeta.ReplyToken != "reply-24" {
		t.Fatalf("WriteFrame() reply token = %q, want reply-24", gotMeta.ReplyToken)
	}
}

func TestSessionWithoutWriterStillMaintainsOutboundLifecycle(t *testing.T) {
	sess := New(Config{ID: 25})
	if err := sess.WriteFrame(&frame.PingPacket{}, nil); err != nil {
		t.Fatalf("WriteFrame() without callback error = %v", err)
	}

	sealer := sess.(OutboundSealer)
	if err := sealer.SealOutboundAndWrite(&frame.EventPacket{Type: "terminal-ack"}, nil); err != nil {
		t.Fatalf("SealOutboundAndWrite() without callback error = %v", err)
	}
	if !sess.(OutboundSealState).OutboundSealed() {
		t.Fatal("OutboundSealed() = false after terminal write without callback")
	}
	if err := sess.WriteFrame(&frame.PongPacket{}); !errors.Is(err, ErrOutboundSealed) {
		t.Fatalf("WriteFrame() after seal error = %v, want %v", err, ErrOutboundSealed)
	}
}

func TestSessionCloseFencesBothWritePaths(t *testing.T) {
	writes := 0
	sess := New(Config{
		ID: 26,
		WriteFrameFn: func(frame.Frame, OutboundMeta) error {
			writes++
			return nil
		},
	})
	if err := sess.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := sess.WriteFrame(&frame.PingPacket{}); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("WriteFrame() after close error = %v, want %v", err, ErrSessionClosed)
	}
	if err := sess.(OutboundSealer).SealOutboundAndWrite(&frame.EventPacket{}); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("SealOutboundAndWrite() after close error = %v, want %v", err, ErrSessionClosed)
	}
	if writes != 0 {
		t.Fatalf("transport callback calls after close = %d, want 0", writes)
	}
}
