package chatlifecycle

import (
	"context"
	"errors"
	"hash/crc32"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

const (
	lifecycleCohortSize = 1_200
	lifecyclePerSlot    = 100
	// LifecycleProofCadence fixes one proof cohort every ten minutes.
	LifecycleProofCadence     = 10 * time.Minute
	lifecycleNaturalQuiet     = 5 * time.Minute
	lifecycleReheatDeadline   = 5 * time.Second
	lifecycleMaxProbeBatch    = 1_200
	lifecycleMaxProbeParallel = 32
)

// LifecycleProofCycleTime returns the exact history-free boundary for a
// zero-based proof cycle. Cycle zero is ten minutes after measured start.
func LifecycleProofCycleTime(start time.Time, cycle uint64) (time.Time, error) {
	if start.IsZero() || cycle == ^uint64(0) {
		return time.Time{}, ErrLifecycleHarnessInvalid
	}
	multiplier := cycle + 1
	if multiplier > uint64(math.MaxInt64/int64(LifecycleProofCadence)) {
		return time.Time{}, ErrLifecycleHarnessInvalid
	}
	deadline := start.Add(time.Duration(multiplier) * LifecycleProofCadence)
	if !deadline.After(start) {
		return time.Time{}, ErrLifecycleHarnessInvalid
	}
	return deadline, nil
}

var (
	// ErrLifecycleHarnessInvalid identifies malformed, incomplete, or transport evidence.
	ErrLifecycleHarnessInvalid = errors.New("chat lifecycle proof: harness invalid")
	// ErrLifecycleProductFailure identifies a proven invalid product transition.
	ErrLifecycleProductFailure = errors.New("chat lifecycle proof: product failure")
)

// LifecycleCandidate is one bounded, transient proof lease. ChannelID is
// deliberately absent from every snapshot and aggregate evidence type.
type LifecycleCandidate struct {
	// ChannelID is the canonical person-channel identity and remains transient.
	ChannelID string `json:"channel_id"`
	// ChannelType is always the person-channel type.
	ChannelType uint8 `json:"channel_type"`
	// HashSlot is the 0-based hash-slot derived from ChannelID.
	HashSlot uint16 `json:"hash_slot"`
	// SlotID is the current 1-based logical Slot assignment for HashSlot.
	SlotID uint32 `json:"slot_id"`
	// TimerToken binds control admission to one generation-local revisit timer.
	// It is transient correlation data and must never enter snapshots or reports.
	TimerToken uint64 `json:"timer_token"`
	// ActivityVersion changes after every successful SENDACK on that timer.
	// It is transient and invalidates an older quiet-window lease.
	ActivityVersion uint64 `json:"activity_version"`
	// InitialSequence is the acknowledged sequence before natural cooling.
	InitialSequence uint64 `json:"initial_sequence"`
	// QuietNotBefore is the earliest valid all-node absence observation.
	QuietNotBefore time.Time `json:"quiet_not_before"`
	// QuietDeadline is the last acceptable time to prove all-node absence.
	QuietDeadline time.Time `json:"quiet_deadline"`
	// ReheatAt is the due time of the already-scheduled revisit SEND.
	ReheatAt time.Time `json:"reheat_at"`
	// ObservedLoaded proves this timer previously had an active runtime.
	ObservedLoaded bool `json:"observed_loaded"`
}

// LifecycleSlotAssignment is an immutable copy of the current 256-to-12 Slot
// assignment. NewLifecycleSlotAssignment accepts a live coordinator mapping;
// callers must not infer ownership from the initial contiguous profile.
type LifecycleSlotAssignment struct {
	slotByHash [formalHashSlots]uint32
	valid      bool
}

// NewLifecycleSlotAssignment validates and copies a complete current mapping.
// All twelve logical Slots must own at least one hash slot.
func NewLifecycleSlotAssignment(slotByHash []uint32) (LifecycleSlotAssignment, error) {
	if len(slotByHash) != formalHashSlots {
		return LifecycleSlotAssignment{}, ErrLifecycleHarnessInvalid
	}
	var seen [formalLogicalSlotGroups]bool
	var assignment LifecycleSlotAssignment
	for hash, slotID := range slotByHash {
		if slotID == 0 || slotID > formalLogicalSlotGroups {
			return LifecycleSlotAssignment{}, ErrLifecycleHarnessInvalid
		}
		assignment.slotByHash[hash] = slotID
		seen[slotID-1] = true
	}
	for _, present := range seen {
		if !present {
			return LifecycleSlotAssignment{}, ErrLifecycleHarnessInvalid
		}
	}
	assignment.valid = true
	return assignment, nil
}

// newInitialLifecycleSlotAssignment constructs the validated no-migration
// profile used by a worker until a live coordinator mapping is injected.
func newInitialLifecycleSlotAssignment() (LifecycleSlotAssignment, error) {
	mapping := make([]uint32, formalHashSlots)
	base := formalHashSlots / formalLogicalSlotGroups
	remainder := formalHashSlots % formalLogicalSlotGroups
	next := 0
	for slotID := 1; slotID <= formalLogicalSlotGroups; slotID++ {
		count := base
		if slotID <= remainder {
			count++
		}
		for index := 0; index < count; index++ {
			mapping[next] = uint32(slotID)
			next++
		}
	}
	return NewLifecycleSlotAssignment(mapping)
}

// HashSlotCount returns 256 only for a successfully constructed assignment.
func (a LifecycleSlotAssignment) HashSlotCount() uint16 {
	if !a.valid {
		return 0
	}
	return formalHashSlots
}

// Lookup returns the copied logical Slot assignment for one hash slot.
func (a LifecycleSlotAssignment) Lookup(hashSlot uint16) (uint32, bool) {
	if !a.valid || hashSlot >= formalHashSlots {
		return 0, false
	}
	return a.slotByHash[hashSlot], true
}

func lifecycleHashSlotForKey(key string, hashSlotCount uint16) uint16 {
	if hashSlotCount == 0 {
		return 0
	}
	return uint16(crc32.ChecksumIEEE([]byte(key)) % uint32(hashSlotCount))
}

// SelectLifecycleCohort validates physical hash-slot ownership and returns
// exactly one hundred candidates for each of twelve logical Slot groups.
func SelectLifecycleCohort(candidates []LifecycleCandidate, now time.Time, assignment LifecycleSlotAssignment, logicalSlots int) ([]LifecycleCandidate, error) {
	if now.IsZero() || assignment.HashSlotCount() != formalHashSlots || logicalSlots != formalLogicalSlotGroups || len(candidates) < lifecycleCohortSize {
		return nil, ErrLifecycleHarnessInvalid
	}
	hashSlots := int(assignment.HashSlotCount())
	bySlot := make([][]LifecycleCandidate, logicalSlots)
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if !validLifecycleCandidate(candidate, now, assignment, hashSlots, logicalSlots) {
			return nil, ErrLifecycleHarnessInvalid
		}
		key := candidate.ChannelID
		if _, duplicate := seen[key]; duplicate {
			return nil, ErrLifecycleHarnessInvalid
		}
		seen[key] = struct{}{}
		bySlot[candidate.SlotID-1] = append(bySlot[candidate.SlotID-1], candidate)
	}
	selected := make([]LifecycleCandidate, 0, lifecycleCohortSize)
	for slotIndex := range bySlot {
		cohort := bySlot[slotIndex]
		if len(cohort) < lifecyclePerSlot {
			return nil, ErrLifecycleHarnessInvalid
		}
		sort.Slice(cohort, func(i, j int) bool {
			if cohort[i].ObservedLoaded != cohort[j].ObservedLoaded {
				return cohort[i].ObservedLoaded
			}
			if !cohort[i].ReheatAt.Equal(cohort[j].ReheatAt) {
				return cohort[i].ReheatAt.Before(cohort[j].ReheatAt)
			}
			return cohort[i].ChannelID < cohort[j].ChannelID
		})
		selected = append(selected, cohort[:lifecyclePerSlot]...)
	}
	return selected, nil
}

func validLifecycleCandidate(candidate LifecycleCandidate, now time.Time, assignment LifecycleSlotAssignment, hashSlots, logicalSlots int) bool {
	slotID, assigned := assignment.Lookup(candidate.HashSlot)
	if candidate.ChannelType != 1 || candidate.TimerToken == 0 || candidate.ActivityVersion == 0 || candidate.InitialSequence == 0 || candidate.SlotID == 0 || int(candidate.SlotID) > logicalSlots ||
		int(candidate.HashSlot) >= hashSlots || lifecycleHashSlotForKey(candidate.ChannelID, uint16(hashSlots)) != candidate.HashSlot ||
		!assigned || slotID != candidate.SlotID || !candidate.QuietNotBefore.After(now) ||
		!candidate.QuietDeadline.After(candidate.QuietNotBefore) || !candidate.ReheatAt.After(candidate.QuietDeadline) {
		return false
	}
	return validLifecyclePersonChannelID(candidate.ChannelID)
}

func validWorkerLifecycleCandidateLease(candidates []LifecycleCandidate, requested int, assignment WorkerAssignment) bool {
	if requested <= 0 || requested > lifecycleCohortSize || len(candidates) > requested ||
		assignment.Config.Workload.Topology.HashSlots != formalHashSlots || assignment.Config.Workload.Topology.LogicalSlotGroups != formalLogicalSlotGroups {
		return false
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if !validWorkerLifecycleCandidate(candidate) {
			return false
		}
		if _, duplicate := seen[candidate.ChannelID]; duplicate {
			return false
		}
		seen[candidate.ChannelID] = struct{}{}
	}
	return true
}

func validWorkerLifecycleCandidate(candidate LifecycleCandidate) bool {
	if candidate.ChannelType != 1 || candidate.TimerToken == 0 || candidate.ActivityVersion == 0 || candidate.InitialSequence == 0 || candidate.SlotID == 0 || candidate.SlotID > formalLogicalSlotGroups ||
		candidate.HashSlot >= formalHashSlots || lifecycleHashSlotForKey(candidate.ChannelID, formalHashSlots) != candidate.HashSlot ||
		candidate.QuietNotBefore.IsZero() || !candidate.QuietDeadline.After(candidate.QuietNotBefore) || !candidate.ReheatAt.After(candidate.QuietDeadline) {
		return false
	}
	return validLifecyclePersonChannelID(candidate.ChannelID)
}

func validLifecyclePersonChannelID(identity string) bool {
	if identity == "" || len(identity) > 512 {
		return false
	}
	left, right, err := channelid.DecodePersonChannel(identity)
	return err == nil && channelid.EncodePersonChannel(left, right) == identity
}

type lifecycleProofPhase uint8

const (
	lifecycleAwaitLoaded lifecycleProofPhase = iota
	lifecycleAwaitAbsent
	lifecycleAwaitReheat
	lifecycleAwaitReloaded
	lifecycleComplete
)

type lifecycleCandidateState struct {
	candidate      LifecycleCandidate
	index          int
	phase          lifecycleProofPhase
	coldObservedAt time.Time
	reheatStarted  time.Time
	lastLEO        [3]uint64
	lastHW         [3]uint64
	lastCheckpoint [3]uint64
	missingSeen    [3]bool
}

// LifecycleProof owns one bounded cohort only for the duration of an explicit
// proof cycle. Its public projection contains aggregate counters, never IDs.
type LifecycleProof struct {
	pollMu     sync.Mutex
	mu         sync.Mutex
	candidates map[string]*lifecycleCandidateState
	order      []*lifecycleCandidateState
	snapshot   LifecycleProofSnapshot
}

// LifecycleProofSnapshot is the bounded identity-free lifecycle projection.
type LifecycleProofSnapshot struct {
	Candidates      uint64                  `json:"candidates"`
	Loaded          uint64                  `json:"loaded"`
	ColdEligible    uint64                  `json:"cold_eligible"`
	Reheated        uint64                  `json:"reheated"`
	Completed       uint64                  `json:"completed"`
	ProductFailures uint64                  `json:"product_failures"`
	HarnessFailures uint64                  `json:"harness_failures"`
	ReheatLatency   WorkerHistogramSnapshot `json:"reheat_latency"`
}

// NewLifecycleProof installs at most one 1,200-row transient candidate cohort.
func NewLifecycleProof(candidates []LifecycleCandidate) (*LifecycleProof, error) {
	if len(candidates) == 0 || len(candidates) > lifecycleCohortSize {
		return nil, ErrLifecycleHarnessInvalid
	}
	states := make(map[string]*lifecycleCandidateState, len(candidates))
	order := make([]*lifecycleCandidateState, 0, len(candidates))
	for index, candidate := range candidates {
		if !validWorkerLifecycleCandidate(candidate) {
			return nil, ErrLifecycleHarnessInvalid
		}
		if _, duplicate := states[candidate.ChannelID]; duplicate {
			return nil, ErrLifecycleHarnessInvalid
		}
		state := &lifecycleCandidateState{candidate: candidate, index: index}
		states[candidate.ChannelID] = state
		order = append(order, state)
	}
	return &LifecycleProof{candidates: states, order: order, snapshot: LifecycleProofSnapshot{
		Candidates: uint64(len(candidates)), ReheatLatency: newWorkerHistogramSnapshot(),
	}}, nil
}

// Observe consumes one all-three-node probe result without retaining its raw identities.
func (p *LifecycleProof) Observe(now time.Time, results []model.ChannelRuntimeProbeResult) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if now.IsZero() || len(results) != 3 {
		p.snapshot.HarnessFailures = saturatingIncrement(p.snapshot.HarnessFailures)
		return ErrLifecycleHarnessInvalid
	}
	orderedResults := append([]model.ChannelRuntimeProbeResult(nil), results...)
	sort.Slice(orderedResults, func(i, j int) bool { return orderedResults[i].NodeID < orderedResults[j].NodeID })
	for index, result := range orderedResults {
		if result.NodeID == 0 {
			p.snapshot.HarnessFailures = saturatingIncrement(p.snapshot.HarnessFailures)
			return ErrLifecycleHarnessInvalid
		}
		if index > 0 && orderedResults[index-1].NodeID == result.NodeID {
			p.snapshot.HarnessFailures = saturatingIncrement(p.snapshot.HarnessFailures)
			return ErrLifecycleHarnessInvalid
		}
	}
	previousSnapshot := p.snapshot
	previousStates := make([]lifecycleCandidateState, len(p.order))
	for index, state := range p.order {
		previousStates[index] = *state
	}
	rollback := func(err error) error {
		for index, state := range p.order {
			*state = previousStates[index]
		}
		p.snapshot = previousSnapshot
		if errors.Is(err, ErrLifecycleProductFailure) {
			p.snapshot.ProductFailures = saturatingIncrement(p.snapshot.ProductFailures)
		} else {
			p.snapshot.HarnessFailures = saturatingIncrement(p.snapshot.HarnessFailures)
		}
		return err
	}
	rows := make([][3]model.ChannelRuntimeProbeChannel, len(p.order))
	seen := make([]uint8, len(p.order))
	for node, result := range orderedResults {
		if result.Checked != len(p.order) || len(result.Channels) != len(p.order) {
			return rollback(ErrLifecycleHarnessInvalid)
		}
		mask := uint8(1 << node)
		for _, row := range result.Channels {
			state := p.candidates[row.ChannelID]
			if state == nil || row.ChannelType != state.candidate.ChannelType || seen[state.index]&mask != 0 {
				return rollback(ErrLifecycleHarnessInvalid)
			}
			rows[state.index][node] = row
			seen[state.index] |= mask
		}
	}
	for index, state := range p.order {
		if seen[index] != 0b111 {
			return rollback(ErrLifecycleHarnessInvalid)
		}
		if err := p.observeCandidateLocked(now, state, rows[index]); err != nil {
			return rollback(err)
		}
	}
	return nil
}

func (p *LifecycleProof) observeCandidateLocked(now time.Time, state *lifecycleCandidateState, rows [3]model.ChannelRuntimeProbeChannel) error {
	allMissing := true
	loadedCount := 0
	leaders := 0
	for index, row := range rows {
		missing := row.Role == "missing" && row.Status == "missing" && row.LEO == 0 && row.HW == 0 && row.CheckpointHW == 0
		allMissing = allMissing && missing
		if missing {
			continue
		}
		if row.Status != "active" || (row.Role != "leader" && row.Role != "follower") || row.HW > row.LEO || row.CheckpointHW > row.HW ||
			row.LEO < state.lastLEO[index] || row.HW < state.lastHW[index] || row.CheckpointHW < state.lastCheckpoint[index] {
			return p.productFailureLocked()
		}
		if row.Role == "leader" {
			leaders++
		}
		loadedCount++
	}
	allLoaded := loadedCount == len(rows)
	switch state.phase {
	case lifecycleAwaitLoaded:
		if !allLoaded || leaders != 1 {
			return p.productFailureLocked()
		}
		for index, row := range rows {
			if row.LEO < state.candidate.InitialSequence || row.HW < state.candidate.InitialSequence {
				return p.productFailureLocked()
			}
			state.lastLEO[index], state.lastHW[index] = row.LEO, row.HW
		}
		state.phase = lifecycleAwaitAbsent
		p.snapshot.Loaded = saturatingIncrement(p.snapshot.Loaded)
	case lifecycleAwaitAbsent:
		if allMissing {
			if now.Before(state.candidate.QuietNotBefore) || now.After(state.candidate.QuietDeadline) {
				return p.productFailureLocked()
			}
			state.phase = lifecycleAwaitReheat
			state.coldObservedAt = now
			p.snapshot.ColdEligible = saturatingIncrement(p.snapshot.ColdEligible)
			return nil
		}
		if !now.Before(state.candidate.QuietDeadline) || leaders > 1 || (allLoaded && leaders != 1) {
			return p.productFailureLocked()
		}
		for index, row := range rows {
			missing := row.Role == "missing"
			if !missing && state.missingSeen[index] {
				return p.productFailureLocked()
			}
			state.missingSeen[index] = state.missingSeen[index] || missing
			if missing {
				continue
			}
			state.lastLEO[index], state.lastHW[index] = row.LEO, row.HW
		}
	case lifecycleAwaitReheat:
		if !now.Before(state.candidate.ReheatAt) {
			return p.harnessFailureLocked()
		}
		if !allMissing {
			return p.productFailureLocked()
		}
	case lifecycleAwaitReloaded:
		if now.After(state.candidate.ReheatAt.Add(lifecycleReheatDeadline)) {
			return p.productFailureLocked()
		}
		if allMissing {
			return nil
		}
		if !allLoaded || leaders != 1 || now.Before(state.reheatStarted) {
			return p.productFailureLocked()
		}
		for _, row := range rows {
			if row.LEO <= state.candidate.InitialSequence || row.HW <= state.candidate.InitialSequence {
				return p.productFailureLocked()
			}
		}
		state.phase = lifecycleComplete
		p.snapshot.Completed = saturatingIncrement(p.snapshot.Completed)
		recordWorkerLatency(&p.snapshot.ReheatLatency, now.Sub(state.reheatStarted))
	case lifecycleComplete:
		if !allLoaded || leaders != 1 {
			return p.productFailureLocked()
		}
	}
	if allLoaded {
		for index, row := range rows {
			state.lastLEO[index], state.lastHW[index], state.lastCheckpoint[index] = row.LEO, row.HW, row.CheckpointHW
		}
	}
	return nil
}

// LifecycleReheatSender is the narrow admission seam for the existing
// deterministic revisit. The worker Engine, not this proof, sends the real SEND.
type LifecycleReheatSender interface {
	ApproveLifecycleReheat(context.Context, LifecycleCandidate) error
}

type lifecycleReheatControl interface {
	ApproveLifecycleReheat(context.Context, WorkerLifecycleReheatRequest) (WorkerLifecycleReheatResponse, error)
}

// WorkerLifecycleReheatSender adapts the authenticated worker client to proof
// admission. It returns no sequence: only the post-reheat all-node probe may
// prove sequence continuity.
type WorkerLifecycleReheatSender struct {
	client lifecycleReheatControl
	fence  WorkerFence
}

func NewWorkerLifecycleReheatSender(client lifecycleReheatControl, fence WorkerFence) (*WorkerLifecycleReheatSender, error) {
	if client == nil || !validWorkerFence(fence) {
		return nil, ErrLifecycleHarnessInvalid
	}
	return &WorkerLifecycleReheatSender{client: client, fence: fence}, nil
}

func (s *WorkerLifecycleReheatSender) ApproveLifecycleReheat(ctx context.Context, candidate LifecycleCandidate) error {
	if ctx == nil || s == nil || s.client == nil || !validWorkerLifecycleCandidate(candidate) {
		return ErrLifecycleHarnessInvalid
	}
	response, err := s.client.ApproveLifecycleReheat(ctx, WorkerLifecycleReheatRequest{
		WorkerFence: s.fence, ChannelID: candidate.ChannelID, TimerToken: candidate.TimerToken, ActivityVersion: candidate.ActivityVersion,
	})
	if err != nil {
		return err
	}
	if !sameWorkerFence(response.WorkerFence, s.fence) || !response.Approved {
		return ErrLifecycleHarnessInvalid
	}
	return nil
}

// Reheat approves the existing timer after cold proof and strictly before its
// deterministic due instant. Completion latency remains based on that due time.
func (p *LifecycleProof) Reheat(ctx context.Context, observedAt time.Time, identity string, sender LifecycleReheatSender) error {
	if ctx == nil || observedAt.IsZero() {
		return p.harnessFailure()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	state := p.candidates[identity]
	if state == nil {
		p.mu.Unlock()
		return p.harnessFailure()
	}
	if state.phase != lifecycleAwaitReheat || sender == nil {
		p.mu.Unlock()
		return p.productFailure()
	}
	if observedAt.Before(state.coldObservedAt) || !observedAt.Before(state.candidate.ReheatAt) {
		p.mu.Unlock()
		return p.harnessFailure()
	}
	candidate := state.candidate
	p.mu.Unlock()
	err := sender.ApproveLifecycleReheat(ctx, candidate)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return p.harnessFailure()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state = p.candidates[identity]
	if state == nil || state.phase != lifecycleAwaitReheat {
		return p.productFailureLocked()
	}
	state.reheatStarted = state.candidate.ReheatAt
	state.phase = lifecycleAwaitReloaded
	p.snapshot.Reheated = saturatingIncrement(p.snapshot.Reheated)
	return nil
}

// ColdEligible reports transient control state and exposes no evidence identity.
func (p *LifecycleProof) ColdEligible(identity string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.candidates[identity]
	return state != nil && (state.phase == lifecycleAwaitReheat || state.phase == lifecycleAwaitReloaded || state.phase == lifecycleComplete)
}

// Snapshot returns an identity-free copy suitable for report aggregation.
func (p *LifecycleProof) Snapshot() LifecycleProofSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshot
}

func (p *LifecycleProof) productFailure() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.productFailureLocked()
}
func (p *LifecycleProof) productFailureLocked() error {
	p.snapshot.ProductFailures = saturatingIncrement(p.snapshot.ProductFailures)
	return ErrLifecycleProductFailure
}
func (p *LifecycleProof) harnessFailure() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.harnessFailureLocked()
}
func (p *LifecycleProof) harnessFailureLocked() error {
	p.snapshot.HarnessFailures = saturatingIncrement(p.snapshot.HarnessFailures)
	return ErrLifecycleHarnessInvalid
}

// LifecycleProbeOptions bounds asynchronous explicit probe work.
type LifecycleProbeOptions struct {
	BatchSize, MaxConcurrency int
	RequestTimeout            time.Duration
}

// LifecycleProbeBatchResult retains only transport aggregates, never probe rows or IDs.
type LifecycleProbeBatchResult struct {
	Requests        uint64
	Nodes           uint64
	TransportErrors uint64
	Latency         WorkerHistogramSnapshot
}

type lifecycleRuntimeProber interface {
	ProbeChannelRuntimeAll(context.Context, model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error)
}

// Poll issues bounded read-only probe batches, merges their transient rows by
// candidate and node, then advances the whole proof in one atomic observation.
// Raw runtime rows never escape this call.
func (p *LifecycleProof) Poll(ctx context.Context, prober lifecycleRuntimeProber, now time.Time, options LifecycleProbeOptions) (LifecycleProbeBatchResult, error) {
	result := LifecycleProbeBatchResult{Latency: newWorkerHistogramSnapshot()}
	if p == nil || now.IsZero() {
		return result, ErrLifecycleHarnessInvalid
	}
	p.pollMu.Lock()
	defer p.pollMu.Unlock()
	p.mu.Lock()
	candidates := make([]LifecycleCandidate, len(p.order))
	for index, state := range p.order {
		candidates[index] = state.candidate
	}
	p.mu.Unlock()
	result, rows, err := probeLifecycleCandidates(ctx, prober, candidates, options)
	if err != nil {
		return result, err
	}
	if err := p.Observe(now, rows); err != nil {
		return result, err
	}
	return result, nil
}

func probeLifecycleCandidates(ctx context.Context, prober lifecycleRuntimeProber, candidates []LifecycleCandidate, options LifecycleProbeOptions) (LifecycleProbeBatchResult, []model.ChannelRuntimeProbeResult, error) {
	result := LifecycleProbeBatchResult{Latency: newWorkerHistogramSnapshot()}
	if options.RequestTimeout == 0 {
		options.RequestTimeout = 5 * time.Second
	}
	if ctx == nil || prober == nil || len(candidates) == 0 || len(candidates) > lifecycleCohortSize || options.BatchSize <= 0 || options.BatchSize > lifecycleMaxProbeBatch ||
		options.MaxConcurrency <= 0 || options.MaxConcurrency > lifecycleMaxProbeParallel || options.RequestTimeout < 0 || options.RequestTimeout > 30*time.Second {
		return result, nil, ErrLifecycleHarnessInvalid
	}
	if err := ctx.Err(); err != nil {
		return result, nil, err
	}
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		batch     int
		requested bool
		nodes     int
		latency   time.Duration
		rows      []model.ChannelRuntimeProbeResult
		err       error
		transport bool
	}
	batches := (len(candidates) + options.BatchSize - 1) / options.BatchSize
	outcomes := make(chan outcome, batches)
	sem := make(chan struct{}, options.MaxConcurrency)
	var wait sync.WaitGroup
	batchIndex := 0
	for start := 0; start < len(candidates); start += options.BatchSize {
		end := start + options.BatchSize
		if end > len(candidates) {
			end = len(candidates)
		}
		batchCandidates := append([]LifecycleCandidate(nil), candidates[start:end]...)
		channels := make([]model.ChannelRuntimeChannelIdentity, len(batchCandidates))
		for index, candidate := range batchCandidates {
			channels[index] = model.ChannelRuntimeChannelIdentity{ChannelID: candidate.ChannelID, ChannelType: candidate.ChannelType}
		}
		wait.Add(1)
		go func(batch int, batchCandidates []LifecycleCandidate, channels []model.ChannelRuntimeChannelIdentity) {
			defer wait.Done()
			select {
			case sem <- struct{}{}:
			case <-probeCtx.Done():
				outcomes <- outcome{batch: batch, err: probeCtx.Err()}
				return
			}
			requestCtx, requestCancel := context.WithTimeout(probeCtx, options.RequestTimeout)
			defer requestCancel()
			started := time.Now()
			rows, err := prober.ProbeChannelRuntimeAll(requestCtx, model.ChannelRuntimeProbeRequest{Channels: channels})
			transport := err != nil
			latency := time.Since(started)
			<-sem
			if err == nil {
				var valid bool
				rows, valid = normalizeLifecycleProbeBatch(rows, batchCandidates)
				if !valid {
					err = ErrLifecycleHarnessInvalid
				}
			}
			outcomes <- outcome{batch: batch, requested: true, nodes: len(rows), latency: latency, rows: rows, err: err, transport: transport}
		}(batchIndex, batchCandidates, channels)
		batchIndex++
	}
	go func() { wait.Wait(); close(outcomes) }()
	var failed bool
	mergedBatches := make([][]model.ChannelRuntimeProbeResult, batches)
	for outcome := range outcomes {
		if outcome.requested {
			result.Requests = saturatingIncrement(result.Requests)
			recordWorkerLatency(&result.Latency, outcome.latency)
		}
		result.Nodes = saturatingAdd(result.Nodes, uint64(outcome.nodes))
		if outcome.err != nil {
			if errors.Is(outcome.err, context.Canceled) && ctx.Err() != nil {
				failed = true
				continue
			}
			if outcome.transport {
				result.TransportErrors = saturatingIncrement(result.TransportErrors)
			}
			failed = true
			cancel()
			continue
		}
		mergedBatches[outcome.batch] = outcome.rows
	}
	if ctx.Err() != nil {
		return result, nil, ctx.Err()
	}
	if failed {
		return result, nil, ErrLifecycleHarnessInvalid
	}
	merged := make([]model.ChannelRuntimeProbeResult, 3)
	for batch, rows := range mergedBatches {
		if len(rows) != 3 {
			return result, nil, ErrLifecycleHarnessInvalid
		}
		for node, row := range rows {
			if batch == 0 {
				merged[node].NodeID = row.NodeID
			} else if merged[node].NodeID != row.NodeID {
				return result, nil, ErrLifecycleHarnessInvalid
			}
			merged[node].Channels = append(merged[node].Channels, row.Channels...)
		}
	}
	for node := range merged {
		if len(merged[node].Channels) != len(candidates) {
			return result, nil, ErrLifecycleHarnessInvalid
		}
		merged[node].Checked = len(candidates)
	}
	return result, merged, nil
}

func normalizeLifecycleProbeBatch(results []model.ChannelRuntimeProbeResult, candidates []LifecycleCandidate) ([]model.ChannelRuntimeProbeResult, bool) {
	if len(results) != 3 {
		return nil, false
	}
	indexByIdentity := make(map[string]int, len(candidates))
	for index, candidate := range candidates {
		if _, duplicate := indexByIdentity[candidate.ChannelID]; duplicate {
			return nil, false
		}
		indexByIdentity[candidate.ChannelID] = index
	}
	ordered := append([]model.ChannelRuntimeProbeResult(nil), results...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].NodeID < ordered[j].NodeID })
	for node, result := range ordered {
		if result.NodeID == 0 || result.Checked != len(candidates) || len(result.Channels) != len(candidates) ||
			(node > 0 && ordered[node-1].NodeID == result.NodeID) {
			return nil, false
		}
		channels := make([]model.ChannelRuntimeProbeChannel, len(candidates))
		seen := make([]bool, len(candidates))
		for _, row := range result.Channels {
			index, exists := indexByIdentity[row.ChannelID]
			if !exists || seen[index] || row.ChannelType != candidates[index].ChannelType {
				return nil, false
			}
			seen[index] = true
			channels[index] = row
		}
		ordered[node].Channels = channels
	}
	return ordered, true
}

// MetaCreateAccounting reconciles checked deterministic unique creation counts
// against exactly three authoritative, slot-partitioned Prometheus scrapes.
type MetaCreateAccounting struct {
	mu                     sync.Mutex
	initialized            bool
	snapshot               MetaCreateAccountingSnapshot
	lastAlready, lastError uint64
}

// MetaCreateAccountingSnapshot is low-cardinality checkpoint evidence.
type MetaCreateAccountingSnapshot struct {
	// ExpectedUnique is the latest deterministic person-edge plus group total.
	ExpectedUnique uint64 `json:"expected_unique"`
	// Created is the latest authoritative cumulative create counter.
	Created uint64 `json:"created"`
	// AlreadyExisting is the latest cumulative concurrent-loser counter.
	AlreadyExisting uint64 `json:"already_existing"`
	// Errors is the latest authoritative create-error counter.
	Errors uint64 `json:"errors"`
	// ReheatCreated accumulates created delta exceeding concurrent expected growth.
	ReheatCreated uint64 `json:"reheat_created"`
	// Checkpoints counts accepted accounting checkpoints.
	Checkpoints uint64 `json:"checkpoints"`
}

func NewMetaCreateAccounting() *MetaCreateAccounting { return &MetaCreateAccounting{} }

// Checkpoint rejects non-integral, regressing, overflowing, or recreating evidence.
func (a *MetaCreateAccounting) Checkpoint(personEdges, preparedGroups uint64, metrics [3]target.MetricsSnapshot, reheat bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	expected, ok := checkedUint64Add(personEdges, preparedGroups)
	if !ok {
		return ErrLifecycleHarnessInvalid
	}
	var created, already, errorsCount uint64
	for _, node := range metrics {
		nodeCreated, ok := exactMetricResultCounter(node, "created")
		if !ok {
			return ErrLifecycleHarnessInvalid
		}
		nodeAlready, ok := exactMetricResultCounter(node, "already_existing")
		if !ok {
			return ErrLifecycleHarnessInvalid
		}
		nodeErrors, ok := exactMetricResultCounter(node, "error")
		if !ok {
			return ErrLifecycleHarnessInvalid
		}
		if created, ok = checkedUint64Add(created, nodeCreated); !ok {
			return ErrLifecycleHarnessInvalid
		}
		if already, ok = checkedUint64Add(already, nodeAlready); !ok {
			return ErrLifecycleHarnessInvalid
		}
		if errorsCount, ok = checkedUint64Add(errorsCount, nodeErrors); !ok {
			return ErrLifecycleHarnessInvalid
		}
	}
	if a.initialized {
		if expected < a.snapshot.ExpectedUnique || created < a.snapshot.Created || already < a.lastAlready || errorsCount < a.lastError {
			return ErrLifecycleHarnessInvalid
		}
		if reheat {
			expectedDelta := expected - a.snapshot.ExpectedUnique
			createdDelta := created - a.snapshot.Created
			if createdDelta > expectedDelta {
				a.snapshot.ReheatCreated = saturatingAdd(a.snapshot.ReheatCreated, createdDelta-expectedDelta)
				return ErrLifecycleProductFailure
			}
			if createdDelta != expectedDelta {
				return ErrLifecycleProductFailure
			}
		}
	} else if reheat {
		return ErrLifecycleHarnessInvalid
	}
	if errorsCount != 0 || created != expected {
		return ErrLifecycleProductFailure
	}
	a.initialized = true
	a.snapshot.ExpectedUnique, a.snapshot.Created, a.snapshot.AlreadyExisting, a.snapshot.Errors = expected, created, already, errorsCount
	a.snapshot.Checkpoints = saturatingIncrement(a.snapshot.Checkpoints)
	a.lastAlready, a.lastError = already, errorsCount
	return nil
}

func (a *MetaCreateAccounting) Snapshot() MetaCreateAccountingSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshot
}

func exactMetricResultCounter(snapshot target.MetricsSnapshot, result string) (uint64, bool) {
	value, exists := snapshot.MetaCreatedTotal[result]
	if !exists {
		return 0, false
	}
	return exactMetricCounter(value)
}

func exactMetricCounter(value float64) (uint64, bool) {
	const maximumExactInteger = float64(uint64(1) << 53)
	return uint64(value), value >= 0 && value <= maximumExactInteger && !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value
}
func checkedUint64Add(left, right uint64) (uint64, bool) {
	if ^uint64(0)-left < right {
		return 0, false
	}
	return left + right, true
}
func saturatingIncrement(value uint64) uint64 {
	if value == ^uint64(0) {
		return value
	}
	return value + 1
}
func saturatingAdd(left, right uint64) uint64 {
	if ^uint64(0)-left < right {
		return ^uint64(0)
	}
	return left + right
}
