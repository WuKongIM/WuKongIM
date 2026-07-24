package opsmcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
	pprofprofile "github.com/google/pprof/profile"
)

const (
	maxProfileRows  = 100
	profileLeaseTTL = 35 * time.Second
)

var errInvalidProfileAnalysis = errors.New("internal/runtime/opsmcp: invalid profile analysis request")

// ProfileAnalysisRequest is the owner-local input for one parsed profile.
type ProfileAnalysisRequest struct {
	// NodeID is the exact target node.
	NodeID uint64
	// Kind is cpu, heap, or goroutine.
	Kind string
	// Seconds is the bounded CPU sampling duration.
	Seconds int
	// SampleType selects a supported heap sample dimension.
	SampleType string
	// Rows is the maximum parsed top-row count.
	Rows int
}

// ProfileAnalysisWindow is the observed capture interval.
type ProfileAnalysisWindow struct {
	// Start is the inclusive capture start.
	Start time.Time
	// End is the inclusive capture end.
	End time.Time
}

// ProfileRPCClient invokes one target node's typed in-memory profile capture.
type ProfileRPCClient interface {
	// CaptureOpsMCPProfile requests one bounded capture from an exact target node.
	CaptureOpsMCPProfile(context.Context, uint64, opscontract.ProfileRequest) (opscontract.ProfileResponse, error)
}

// ProfileAnalyzer enforces the cluster-wide one-at-a-time gate and parses raw
// target RPC bytes into bounded symbolic rows.
type ProfileAnalyzer struct {
	// state supplies the latest Controller-derived owner, revision, and fence.
	state StateReader
	// client invokes the exact target node after the owner creates a lease.
	client ProfileRPCClient
	// localNodeID identifies the only node allowed to authorize owner leases.
	localNodeID uint64
	// now provides deterministic lease and profile-window timestamps.
	now func() time.Time

	// mu protects active and lease for the complete owner-local capture lifecycle.
	mu sync.Mutex
	// active enforces one cluster-wide capture for this owner generation.
	active bool
	// lease is the current unpredictable, single-use target authorization.
	lease profileLease
}

type profileLease struct {
	// id is an unpredictable owner-held authorization identifier.
	id string
	// targetNodeID binds the lease to one exact profiling target.
	targetNodeID uint64
	// revision binds the lease to one Controller MCP generation.
	revision uint64
	// expiresAt bounds how long the target may consume the lease.
	expiresAt time.Time
	// used prevents replay after one successful authorization.
	used bool
}

// NewProfileAnalyzer creates the owner-local cluster profile analyzer.
func NewProfileAnalyzer(state StateReader, client ProfileRPCClient, localNodeID uint64) *ProfileAnalyzer {
	return &ProfileAnalyzer{state: state, client: client, localNodeID: localNodeID, now: time.Now}
}

// AnalyzeOpsProfile captures, parses, and discards one profile.
func (a *ProfileAnalyzer) AnalyzeOpsProfile(ctx context.Context, request ProfileAnalysisRequest) (any, *ProfileAnalysisWindow, error) {
	if a == nil || a.state == nil || a.client == nil || a.localNodeID == 0 || request.NodeID == 0 ||
		request.Rows < 1 || request.Rows > maxProfileRows {
		return nil, nil, errInvalidProfileAnalysis
	}
	state, err := a.state.OpsMCPDesiredState(ctx)
	if err != nil {
		return nil, nil, ErrOwnerUnavailable
	}
	if !state.Enabled {
		return nil, nil, ErrDisabled
	}
	if state.OwnerNodeID != a.localNodeID {
		return nil, nil, ErrOwnerUnavailable
	}
	if fence := state.ProfileFenceUntilUnixMillis; fence > 0 && a.now().UTC().UnixMilli() < fence {
		return nil, nil, ErrProfileCooldown
	}
	start := a.now().UTC()
	leaseID, err := newProfileLeaseID()
	if err != nil {
		return nil, nil, ErrOwnerUnavailable
	}
	a.mu.Lock()
	if a.active {
		a.mu.Unlock()
		return nil, nil, ErrProfileBusy
	}
	a.active = true
	a.lease = profileLease{
		id: leaseID, targetNodeID: request.NodeID, revision: state.Revision,
		expiresAt: start.Add(profileLeaseTTL),
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.active = false
		a.lease = profileLease{}
		a.mu.Unlock()
	}()

	seconds := request.Seconds
	if request.Kind != "cpu" {
		seconds = 0
	}
	response, err := a.client.CaptureOpsMCPProfile(ctx, request.NodeID, opscontract.ProfileRequest{
		Version: opscontract.RPCVersion, OwnerNodeID: a.localNodeID, ExpectedRevision: state.Revision,
		NodeID: request.NodeID, Kind: request.Kind, Seconds: seconds, LeaseID: leaseID,
	})
	if err != nil {
		return nil, nil, err
	}
	parsed, err := pprofprofile.ParseData(response.Payload)
	if err != nil {
		return nil, nil, errors.New("internal/runtime/opsmcp: invalid target profile")
	}
	rows, err := summarizeProfile(parsed, request.Rows, request.SampleType, request.Kind)
	if err != nil {
		return nil, nil, err
	}
	end := a.now().UTC()
	return profileAnalysis{
		NodeID: request.NodeID, Kind: request.Kind, SampleType: sampleTypeOf(rows),
		Rows: rows,
	}, &ProfileAnalysisWindow{Start: start, End: end}, nil
}

// AuthorizeProfileLease consumes one exact owner-generated profile lease.
// The lease exists only in owner memory and cannot be reconstructed from
// Controller state or a caller-supplied node identity.
func (a *ProfileAnalyzer) AuthorizeProfileLease(ctx context.Context, request opscontract.ProfileLeaseRequest) error {
	if a == nil || a.state == nil || a.localNodeID == 0 ||
		request.Version != opscontract.RPCVersion || request.OwnerNodeID != a.localNodeID ||
		request.TargetNodeID == 0 || !validProfileLeaseID(request.LeaseID) {
		return ErrUnauthorized
	}
	state, err := a.state.OpsMCPDesiredState(ctx)
	if err != nil {
		return ErrOwnerUnavailable
	}
	if !state.Enabled {
		return ErrDisabled
	}
	if state.Revision != request.ExpectedRevision {
		return ErrStateChanged
	}
	if state.OwnerNodeID != a.localNodeID {
		return ErrOwnerUnavailable
	}
	now := a.now().UTC()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active || a.lease.used || a.lease.id != request.LeaseID ||
		a.lease.targetNodeID != request.TargetNodeID || a.lease.revision != request.ExpectedRevision ||
		!now.Before(a.lease.expiresAt) {
		return ErrUnauthorized
	}
	a.lease.used = true
	return nil
}

func newProfileLeaseID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

func validProfileLeaseID(value string) bool {
	if len(value) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16
}

// ProfileRow is one bounded symbolic pprof aggregate.
type ProfileRow struct {
	// Function is a bounded symbolic function name.
	Function string `json:"function"`
	// Flat is the sample value attributed directly to Function.
	Flat int64 `json:"flat"`
	// Cumulative is the sample value including callees.
	Cumulative int64 `json:"cumulative"`
	// Unit describes Flat and Cumulative.
	Unit string `json:"unit"`
	// SampleType identifies the selected pprof sample dimension.
	SampleType string `json:"sample_type"`
}

type profileAnalysis struct {
	NodeID     uint64       `json:"node_id"`
	Kind       string       `json:"kind"`
	SampleType string       `json:"sample_type"`
	Rows       []ProfileRow `json:"rows"`
}

func summarizeProfile(profile *pprofprofile.Profile, limit int, requested, kind string) ([]ProfileRow, error) {
	if profile == nil || len(profile.SampleType) == 0 {
		return []ProfileRow{}, nil
	}
	if requested == "" && kind == "heap" {
		requested = "inuse_space"
	}
	valueIndex := len(profile.SampleType) - 1
	if requested != "" {
		valueIndex = -1
		for index, sampleType := range profile.SampleType {
			if sampleType != nil && sampleType.Type == requested {
				valueIndex = index
				break
			}
		}
		if valueIndex < 0 {
			return nil, errInvalidProfileAnalysis
		}
	}
	type aggregate struct {
		flat       int64
		cumulative int64
	}
	aggregates := make(map[string]aggregate)
	for _, sample := range profile.Sample {
		if valueIndex >= len(sample.Value) {
			continue
		}
		value := sample.Value[valueIndex]
		seen := make(map[string]struct{})
		for locationIndex, location := range sample.Location {
			for _, line := range location.Line {
				if line.Function == nil || line.Function.Name == "" {
					continue
				}
				name := line.Function.Name
				item := aggregates[name]
				if locationIndex == 0 {
					item.flat += value
				}
				if _, found := seen[name]; !found {
					item.cumulative += value
					seen[name] = struct{}{}
				}
				aggregates[name] = item
			}
		}
	}
	sampleType := profile.SampleType[valueIndex].Type
	unit := profile.SampleType[valueIndex].Unit
	rows := make([]ProfileRow, 0, len(aggregates))
	for function, item := range aggregates {
		rows = append(rows, ProfileRow{
			Function: function, Flat: item.flat, Cumulative: item.cumulative,
			Unit: unit, SampleType: sampleType,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Cumulative != rows[j].Cumulative {
			return rows[i].Cumulative > rows[j].Cumulative
		}
		return rows[i].Function < rows[j].Function
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func sampleTypeOf(rows []ProfileRow) string {
	if len(rows) == 0 {
		return ""
	}
	return rows[0].SampleType
}
