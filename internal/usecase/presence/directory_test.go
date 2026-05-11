package presence

import (
	"encoding/binary"
	"hash"
	"hash/fnv"
	"testing"
)

var routeFingerprintSink uint64

func TestRouteFingerprintMatchesFNV64AReference(t *testing.T) {
	route := Route{
		UID:         "user-123",
		DeviceID:    "device-abc",
		SessionID:   42,
		DeviceFlag:  1,
		DeviceLevel: 2,
		Listener:    "tcp",
	}

	if got, want := routeFingerprint(route), referenceRouteFingerprint(route); got != want {
		t.Fatalf("routeFingerprint() = %d, want %d", got, want)
	}
}

func TestRouteFingerprintDoesNotAllocate(t *testing.T) {
	route := Route{
		UID:         "user-123",
		DeviceID:    "device-abc",
		SessionID:   42,
		DeviceFlag:  1,
		DeviceLevel: 2,
		Listener:    "tcp",
	}

	allocs := testing.AllocsPerRun(1000, func() {
		routeFingerprintSink = routeFingerprint(route)
	})
	if allocs != 0 {
		t.Fatalf("routeFingerprint allocations = %.0f, want 0", allocs)
	}
}

func referenceRouteFingerprint(route Route) uint64 {
	h := fnv.New64a()
	referenceWriteUint64(h, route.SessionID)
	referenceWriteString(h, route.UID)
	referenceWriteString(h, route.DeviceID)
	referenceWriteUint64(h, uint64(route.DeviceFlag))
	referenceWriteUint64(h, uint64(route.DeviceLevel))
	referenceWriteString(h, route.Listener)
	return h.Sum64()
}

func referenceWriteUint64(h hash.Hash64, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	_, _ = h.Write(buf[:])
}

func referenceWriteString(h hash.Hash64, value string) {
	_, _ = h.Write([]byte(value))
	_, _ = h.Write([]byte{0})
}
