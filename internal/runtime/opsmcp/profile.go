package opsmcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	runtimepprof "runtime/pprof"
	"sync"
	"time"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
)

const profileCooldown = 60 * time.Second

var (
	// ErrProfileBusy reports an overlapping target-local profile capture.
	ErrProfileBusy = errors.New("internal/runtime/opsmcp: profile capture busy")
	// ErrProfileCooldown reports a capture inside the target-local cooldown.
	ErrProfileCooldown = errors.New("internal/runtime/opsmcp: profile capture cooldown")
	// ErrProfileTooLarge reports an in-memory profile above the RPC bound.
	ErrProfileTooLarge = errors.New("internal/runtime/opsmcp: profile too large")
)

// Profiler captures bounded runtime profiles after desired-state fencing.
type Profiler struct {
	// state supplies the latest Controller-derived owner and revision fence.
	state StateReader
	// leases verifies a single-use authorization with the configured owner.
	leases ProfileLeaseVerifier
	// localNodeID binds capture requests to this exact target node.
	localNodeID uint64
	// now provides deterministic cooldown timestamps.
	now func() time.Time

	// mu protects active and lastCompleted across the full target capture lifecycle.
	mu sync.Mutex
	// active prevents overlapping target-local runtime profile captures.
	active bool
	// lastCompleted enforces a full target-local cooldown after capture completion.
	lastCompleted time.Time
}

// ProfileLeaseVerifier proves that the configured owner issued one exact,
// unconsumed profile authorization for this target.
type ProfileLeaseVerifier interface {
	// VerifyOpsMCPProfileLease consumes one exact owner-held target authorization.
	VerifyOpsMCPProfileLease(context.Context, uint64, opscontract.ProfileLeaseRequest) error
}

// NewProfiler creates a target-local in-memory profile runtime.
func NewProfiler(state StateReader, leases ProfileLeaseVerifier, localNodeID uint64) *Profiler {
	return &Profiler{state: state, leases: leases, localNodeID: localNodeID, now: time.Now}
}

// CaptureProfile verifies the configured owner and captures no persistent artifact.
func (p *Profiler) CaptureProfile(ctx context.Context, request opscontract.ProfileRequest) (opscontract.ProfileResponse, error) {
	if p == nil || p.state == nil || p.leases == nil || request.Version != opscontract.RPCVersion ||
		request.NodeID == 0 || request.NodeID != p.localNodeID || request.OwnerNodeID == 0 ||
		!validProfileRequest(request) || !validProfileLeaseID(request.LeaseID) {
		return opscontract.ProfileResponse{}, ErrUnauthorized
	}
	state, err := p.state.OpsMCPDesiredState(ctx)
	if err != nil {
		return opscontract.ProfileResponse{}, ErrOwnerUnavailable
	}
	if !state.Enabled {
		return opscontract.ProfileResponse{}, ErrDisabled
	}
	if state.Revision != request.ExpectedRevision {
		return opscontract.ProfileResponse{}, ErrStateChanged
	}
	if state.OwnerNodeID != request.OwnerNodeID {
		return opscontract.ProfileResponse{}, ErrUnauthorized
	}
	if err := p.leases.VerifyOpsMCPProfileLease(ctx, request.OwnerNodeID, opscontract.ProfileLeaseRequest{
		Version: request.Version, OwnerNodeID: request.OwnerNodeID, ExpectedRevision: request.ExpectedRevision,
		TargetNodeID: request.NodeID, LeaseID: request.LeaseID,
	}); err != nil {
		return opscontract.ProfileResponse{}, err
	}
	now := p.now().UTC()
	p.mu.Lock()
	if p.active {
		p.mu.Unlock()
		return opscontract.ProfileResponse{}, ErrProfileBusy
	}
	if !p.lastCompleted.IsZero() && now.Sub(p.lastCompleted) < profileCooldown {
		p.mu.Unlock()
		return opscontract.ProfileResponse{}, ErrProfileCooldown
	}
	p.active = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.active = false
		p.lastCompleted = p.now().UTC()
		p.mu.Unlock()
	}()

	payload, err := captureRuntimeProfile(ctx, request)
	if err != nil {
		return opscontract.ProfileResponse{}, err
	}
	return opscontract.ProfileResponse{Version: opscontract.RPCVersion, Payload: payload}, nil
}

func validProfileRequest(request opscontract.ProfileRequest) bool {
	switch request.Kind {
	case "cpu":
		return request.Seconds >= 1 && request.Seconds <= 30
	case "heap", "goroutine":
		return request.Seconds == 0
	default:
		return false
	}
}

func captureRuntimeProfile(ctx context.Context, request opscontract.ProfileRequest) ([]byte, error) {
	writer := &profileLimitWriter{remaining: opscontract.MaxProfileBytes}
	switch request.Kind {
	case "cpu":
		if err := runtimepprof.StartCPUProfile(writer); err != nil {
			return nil, err
		}
		timer := time.NewTimer(time.Duration(request.Seconds) * time.Second)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			runtimepprof.StopCPUProfile()
			return nil, ctx.Err()
		case <-timer.C:
			runtimepprof.StopCPUProfile()
		}
	case "heap":
		runtime.GC()
		if err := runtimepprof.Lookup("heap").WriteTo(writer, 0); err != nil {
			return nil, err
		}
	case "goroutine":
		if err := runtimepprof.Lookup("goroutine").WriteTo(writer, 0); err != nil {
			return nil, err
		}
	default:
		return nil, ErrUnauthorized
	}
	if writer.exceeded {
		return nil, ErrProfileTooLarge
	}
	return append([]byte(nil), writer.buffer.Bytes()...), nil
}

type profileLimitWriter struct {
	buffer    bytes.Buffer
	remaining int
	exceeded  bool
}

func (w *profileLimitWriter) Write(payload []byte) (int, error) {
	if len(payload) > w.remaining {
		w.exceeded = true
		return 0, fmt.Errorf("%w: %d bytes", ErrProfileTooLarge, len(payload))
	}
	w.remaining -= len(payload)
	return w.buffer.Write(payload)
}
