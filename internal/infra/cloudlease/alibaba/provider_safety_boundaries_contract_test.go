package alibaba

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"
)

func TestProviderCostArithmeticRoundsUpAndRejectsOverflow(t *testing.T) {
	if got, ok := ceilingUnits(61*time.Minute, time.Hour); !ok || got != 2 {
		t.Fatalf("ceilingUnits(61m, 1h) = %d/%v", got, ok)
	}
	if _, ok := ceilingUnits(0, time.Hour); ok {
		t.Fatal("ceilingUnits accepted a zero billing duration")
	}
	if got, ok := ceilingBytes(10<<30+1, 1<<30); !ok || got != 11 {
		t.Fatalf("ceilingBytes(10 GiB + 1) = %d/%v", got, ok)
	}
	if _, ok := ceilingBytes(math.MaxInt64, 2); ok {
		t.Fatal("ceilingBytes accepted overflowing round-up arithmetic")
	}
	if got, ok := checkedIntMultiply(3, 4); !ok || got != 12 {
		t.Fatalf("checkedIntMultiply(3,4) = %d/%v", got, ok)
	}
	if _, ok := checkedIntMultiply(math.MaxInt, 2); ok {
		t.Fatal("checkedIntMultiply accepted overflow")
	}
	if got, ok := checkedMultiply(3, 4); !ok || got != 12 {
		t.Fatalf("checkedMultiply(3,4) = %d/%v", got, ok)
	}
	if _, ok := checkedMultiply(math.MaxInt64, 2); ok {
		t.Fatal("checkedMultiply accepted overflow")
	}
	if got, ok := checkedAdd(3, 4); !ok || got != 7 {
		t.Fatalf("checkedAdd(3,4) = %d/%v", got, ok)
	}
	if _, ok := checkedAdd(math.MaxInt64, 1); ok {
		t.Fatal("checkedAdd accepted overflow")
	}
	if offerIdentity(offer{zone: "ab", instanceType: "c"}) == offerIdentity(offer{zone: "a", instanceType: "bc"}) {
		t.Fatal("offer identity allowed zone/type boundary collisions")
	}
	if hostName := openAPIHostName("lease-contract", strings.Repeat("service", 12), 1); len(hostName) != 63 {
		t.Fatalf("provider host name length = %d, want Alibaba maximum 63", len(hostName))
	}
}

func TestLifecycleWaitHonorsCancellationAndImmediateCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitContext(ctx, time.Hour); err != context.Canceled {
		t.Fatalf("waitContext(canceled) error = %v", err)
	}
	if err := waitContext(context.Background(), 0); err != nil {
		t.Fatalf("waitContext(immediate) error = %v", err)
	}
}
