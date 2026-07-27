package opsmcp

import (
	"context"
	"errors"
	"testing"
	"time"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
	pprofprofile "github.com/google/pprof/profile"
)

func TestProfilerVerifiesOwnerRevisionAndReturnsParseableInMemoryProfile(t *testing.T) {
	state := stateReaderStub{state: DesiredState{Revision: 9, Enabled: true, OwnerNodeID: 1}}
	leases := &profileLeaseVerifierStub{}
	profiler := NewProfiler(state, leases, 3)
	profiler.now = func() time.Time { return time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC) }
	request := opscontract.ProfileRequest{
		Version: opscontract.RPCVersion, OwnerNodeID: 1, ExpectedRevision: 9,
		NodeID: 3, Kind: "goroutine", LeaseID: "0123456789abcdef0123456789abcdef",
	}
	got, err := profiler.CaptureProfile(context.Background(), request)
	if err != nil {
		t.Fatalf("CaptureProfile() error = %v", err)
	}
	if _, err := pprofprofile.ParseData(got.Payload); err != nil {
		t.Fatalf("profile is not parseable: %v", err)
	}
	if _, err := profiler.CaptureProfile(context.Background(), request); !errors.Is(err, ErrProfileCooldown) {
		t.Fatalf("second capture error = %v, want cooldown", err)
	}
	if leases.request.LeaseID != "0123456789abcdef0123456789abcdef" || leases.ownerNodeID != 1 {
		t.Fatalf("lease verification = owner %d request %#v", leases.ownerNodeID, leases.request)
	}
}

func TestProfilerRejectsNonOwnerAndStaleRevision(t *testing.T) {
	state := stateReaderStub{state: DesiredState{Revision: 9, Enabled: true, OwnerNodeID: 1}}
	request := opscontract.ProfileRequest{
		Version: opscontract.RPCVersion, OwnerNodeID: 2, ExpectedRevision: 9,
		NodeID: 3, Kind: "heap", LeaseID: "0123456789abcdef0123456789abcdef",
	}
	leases := &profileLeaseVerifierStub{}
	if _, err := NewProfiler(state, leases, 3).CaptureProfile(context.Background(), request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("non-owner error = %v", err)
	}
	request.OwnerNodeID = 1
	request.ExpectedRevision = 8
	if _, err := NewProfiler(state, leases, 3).CaptureProfile(context.Background(), request); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("stale revision error = %v", err)
	}
}

func TestProfilerRejectsForgedOwnerWithoutValidLease(t *testing.T) {
	state := stateReaderStub{state: DesiredState{Revision: 9, Enabled: true, OwnerNodeID: 1}}
	leases := &profileLeaseVerifierStub{err: ErrUnauthorized}
	profiler := NewProfiler(state, leases, 3)
	request := opscontract.ProfileRequest{
		Version: opscontract.RPCVersion, OwnerNodeID: 1, ExpectedRevision: 9,
		NodeID: 3, Kind: "goroutine", LeaseID: "fedcba9876543210fedcba9876543210",
	}
	if _, err := profiler.CaptureProfile(context.Background(), request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("forged lease error = %v, want unauthorized", err)
	}
	if !profiler.lastCompleted.IsZero() {
		t.Fatal("forged lease reached profile admission")
	}
}

func TestProfilerCooldownStartsAfterCaptureCompletion(t *testing.T) {
	state := stateReaderStub{state: DesiredState{Revision: 9, Enabled: true, OwnerNodeID: 1}}
	profiler := NewProfiler(state, &profileLeaseVerifierStub{}, 3)
	start := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	timestamps := []time.Time{start, start.Add(30 * time.Second)}
	profiler.now = func() time.Time {
		if len(timestamps) == 0 {
			return start.Add(89 * time.Second)
		}
		value := timestamps[0]
		timestamps = timestamps[1:]
		return value
	}
	request := opscontract.ProfileRequest{
		Version: opscontract.RPCVersion, OwnerNodeID: 1, ExpectedRevision: 9,
		NodeID: 3, Kind: "goroutine", LeaseID: "0123456789abcdef0123456789abcdef",
	}

	if _, err := profiler.CaptureProfile(context.Background(), request); err != nil {
		t.Fatalf("first CaptureProfile() error = %v", err)
	}
	if want := start.Add(30 * time.Second); !profiler.lastCompleted.Equal(want) {
		t.Fatalf("lastCompleted = %v, want %v", profiler.lastCompleted, want)
	}
	if _, err := profiler.CaptureProfile(context.Background(), request); !errors.Is(err, ErrProfileCooldown) {
		t.Fatalf("capture 59 seconds after completion error = %v, want cooldown", err)
	}
	profiler.now = func() time.Time { return start.Add(90 * time.Second) }
	if _, err := profiler.CaptureProfile(context.Background(), request); err != nil {
		t.Fatalf("capture 60 seconds after completion error = %v", err)
	}
}

type profileLeaseVerifierStub struct {
	ownerNodeID uint64
	request     opscontract.ProfileLeaseRequest
	err         error
}

func (s *profileLeaseVerifierStub) VerifyOpsMCPProfileLease(_ context.Context, ownerNodeID uint64, request opscontract.ProfileLeaseRequest) error {
	s.ownerNodeID = ownerNodeID
	s.request = request
	return s.err
}
