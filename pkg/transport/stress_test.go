package transport_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/transport/testkit"
)

func TestTransportStressMixedRPCAndSend(t *testing.T) {
	if os.Getenv("WK_TRANSPORT_STRESS") != "1" && os.Getenv("WK_TRANSPORTV2_STRESS") != "1" {
		t.Skip("set WK_TRANSPORT_STRESS=1 to run transport stress test")
	}

	var received atomic.Int64
	h := testkit.NewHarness(t, func(ctx context.Context, payload []byte) ([]byte, error) {
		received.Add(1)
		return payload, nil
	})
	defer h.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := []byte("stress")
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				shardKey := uint64(worker)
				if _, err := h.Client.Call(ctx, testkit.ServerNodeID, shardKey, transport.PriorityRPC, testkit.ServiceID, payload); err != nil && ctx.Err() == nil {
					t.Errorf("Call() error = %v", err)
					cancel()
					return
				}
				if err := h.Client.Send(ctx, testkit.ServerNodeID, shardKey, transport.PriorityControl, testkit.ServiceID, payload); err != nil && ctx.Err() == nil {
					t.Errorf("Send() error = %v", err)
					cancel()
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := received.Load(); got == 0 {
		t.Fatal("handler received no requests")
	}
}
