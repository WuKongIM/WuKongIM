package chatlifecycle

import (
	"container/heap"
	"errors"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	logicalOrdinalBits    = 43
	logicalGenerationBits = 18
	maxLogicalOrdinal     = uint64(1)<<logicalOrdinalBits - 1
	maxLogicalGeneration  = uint64(1)<<logicalGenerationBits - 1
)

func scopedLogicalOrdinal(generation uint64, domain LogicalDomain, ordinal uint64) (uint64, error) {
	if generation == 0 || generation > maxLogicalGeneration || domain < LogicalDomainPrimary || domain > LogicalDomainCanary || ordinal > maxLogicalOrdinal {
		return 0, errTrafficGeneratorConfig
	}
	return uint64(domain)<<(logicalGenerationBits+logicalOrdinalBits) |
		generation<<logicalOrdinalBits | ordinal, nil
}

// LogicalDomain partitions the payload-visible uint64 identity space. The
// 43-bit ordinal budget holds over 8.7e12 messages per domain/generation;
// 72 hours at 2,000 SEND/s uses about 5.2e8.
type LogicalDomain uint8

const (
	LogicalDomainPrimary LogicalDomain = iota + 1
	LogicalDomainLifecycle
	LogicalDomainRevisit
	LogicalDomainGroup
	LogicalDomainCanary
)

var (
	errTrafficGeneratorConfig = errors.New("chat lifecycle traffic runtime: configuration is incomplete")
	errTrafficEmitter         = errors.New("chat lifecycle traffic runtime: emitter is required")
	errRetrySchedulerConfig   = errors.New("chat lifecycle retry runtime: configuration is invalid")
	errRetryAlreadyScheduled  = errors.New("chat lifecycle retry runtime: logical send already has a scheduled retry")
	// ErrRetryLimitReached proves that attempt three was the final approved retry.
	ErrRetryLimitReached = errors.New("chat lifecycle retry runtime: retry limit reached")
)

// RuntimeFailureCode is the closed worker-owned failure vocabulary. These
// failures are harness-invalid and must not be attributed to the target.
type RuntimeFailureCode string

const (
	RuntimeFailureRetryQueueSaturated   RuntimeFailureCode = "retry_queue_saturated"
	RuntimeFailureEngineQueueSaturated  RuntimeFailureCode = "engine_queue_saturated"
	RuntimeFailureEngineCPUSaturated    RuntimeFailureCode = "engine_cpu_saturated"
	RuntimeFailureInflightSaturated     RuntimeFailureCode = "engine_inflight_saturated"
	RuntimeFailureLoginSaturated        RuntimeFailureCode = "session_login_saturated"
	RuntimeFailureUnderDelivery         RuntimeFailureCode = "offered_load_under_delivery"
	RuntimeFailureSchedulerCPUSaturated RuntimeFailureCode = "session_scheduler_cpu_saturated"
	RuntimeFailureClockMovedBackwards   RuntimeFailureCode = "engine_clock_moved_backwards"
)

// RuntimeError is redacted worker-runtime evidence with harness ownership.
type RuntimeError struct {
	code RuntimeFailureCode
}

func (e *RuntimeError) Error() string {
	if e == nil {
		return "chat lifecycle runtime failed"
	}
	return "chat lifecycle runtime failed: " + string(e.code)
}

// Classification always attributes local saturation to the harness.
func (e *RuntimeError) Classification() SyncClassification {
	if e == nil {
		return ""
	}
	return SyncClassificationHarnessInvalid
}

// Code returns the fixed redacted reason.
func (e *RuntimeError) Code() RuntimeFailureCode {
	if e == nil {
		return ""
	}
	return e.code
}

// TrafficGeneratorConfig binds the existing deterministic Phase 2 models.
type TrafficGeneratorConfig struct {
	Identity *IdentitySpace
	Model    TrafficModel
	Catalog  GroupCatalog
	Workload WorkloadConfig
	Start    time.Time
	// WorkerID is this generator's zero-based worker partition.
	WorkerID uint64
	// WorkerCount must match both Workload.Workers and Identity.Workers.
	WorkerCount uint64
}

// TrafficIntent is one transient SEND. It contains no target mutation path;
// an engine may only submit Packet through an online WKProto session.
type TrafficIntent struct {
	Logical       LogicalSend
	Packet        *frame.SendPacket
	Kind          TrafficKind
	Direction     PersonDirection
	ChannelID     string
	GroupCategory GroupCategory
	PayloadBytes  int
	Canary        bool
	Domain        LogicalDomain
}

// TrafficTickSnapshot is one streaming aggregate; it never retains intents.
type TrafficTickSnapshot struct {
	Released      uint64
	Person        uint64
	Group         uint64
	PayloadCounts [4]uint64
	PayloadBytes  uint64
}

// Add folds another tick into this snapshot without retaining its work.
func (s *TrafficTickSnapshot) Add(other TrafficTickSnapshot) {
	s.Released += other.Released
	s.Person += other.Person
	s.Group += other.Group
	for index := range s.PayloadCounts {
		s.PayloadCounts[index] += other.PayloadCounts[index]
	}
	s.PayloadBytes += other.PayloadBytes
}

// TrafficGeneratorSnapshot exposes only counters and the fixed hot-set bound.
type TrafficGeneratorSnapshot struct {
	PrimaryReleased uint64
	Person          uint64
	Group           uint64
	Canaries        uint64
	PayloadBytes    uint64
	// HotSet contains this worker's person-channel limit and the fixed global
	// group catalog size; person limits sum exactly to the configured target.
	HotSet GroupHotSet
}

// TrafficGenerator streams deterministic work from one global allocator. One
// engine goroutine owns it; it is intentionally not concurrently mutable.
type TrafficGenerator struct {
	identity  *IdentitySpace
	model     TrafficModel
	catalog   GroupCatalog
	workload  WorkloadConfig
	allocator *RateAllocator
	hotSet    GroupHotSet
	workerID  uint64
	workers   uint64

	primaryOrdinal uint64
	canaryOrdinal  uint64
	nextCanary     time.Time
	generation     uint64
	snapshot       TrafficGeneratorSnapshot
}

// NewTrafficGenerator constructs fixed-size allocator and catalog state.
func NewTrafficGenerator(config TrafficGeneratorConfig) (*TrafficGenerator, error) {
	if config.Identity == nil || config.Model.identity != config.Identity ||
		config.Catalog.identity != config.Identity || config.Workload.Workers <= 0 || config.Workload.SendRatePerSecond <= 0 ||
		config.Workload.MaxGlobalBurst != 2*config.Workload.SendRatePerSecond || config.Start.IsZero() ||
		config.WorkerCount != uint64(config.Workload.Workers) || config.WorkerCount != config.Identity.Workers() ||
		config.WorkerID >= config.WorkerCount {
		return nil, errTrafficGeneratorConfig
	}
	// Every primary category must have at least one fixed group per worker;
	// otherwise exact category shares and single-owner routing cannot coexist.
	for category, weight := range config.Catalog.primaryWeight {
		if weight > 0 && config.Catalog.counts[category] < int(config.WorkerCount) {
			return nil, errTrafficGeneratorConfig
		}
	}
	weights := make([]int64, config.Workload.Workers)
	for index := range weights {
		weights[index] = 1
	}
	allocator, err := NewRateAllocator(uint64(config.Workload.SendRatePerSecond), uint64(config.Workload.MaxGlobalBurst), weights)
	if err != nil {
		return nil, err
	}
	globalHotSet, err := config.Catalog.HotSet(config.Workload.HotSet.PersonChannels)
	if err != nil || globalHotSet.GroupChannels != config.Workload.HotSet.GroupChannels {
		return nil, errTrafficGeneratorConfig
	}
	personChannels, err := workerPersonHotSetTarget(config.Workload.HotSet.PersonChannels, config.WorkerID, config.WorkerCount)
	if err != nil {
		return nil, err
	}
	hotSet := globalHotSet
	hotSet.PersonChannels = personChannels
	hotSet.TotalChannels = personChannels + hotSet.GroupChannels
	generator := &TrafficGenerator{
		identity: config.Identity, model: config.Model, catalog: config.Catalog,
		workload: config.Workload, allocator: allocator, hotSet: hotSet,
		workerID: config.WorkerID, workers: config.WorkerCount,
		generation: 1,
	}
	if config.Workload.Groups.VeryLarge > 0 {
		generator.nextCanary = config.Start.Add(config.Workload.Groups.VeryLargeSendEvery)
	}
	generator.snapshot.HotSet = hotSet
	return generator, nil
}

func workerPersonHotSetTarget(total int, workerID, workerCount uint64) (int, error) {
	if total <= 0 || workerCount == 0 || workerID >= workerCount {
		return 0, errTrafficGeneratorConfig
	}
	target := total / int(workerCount)
	if workerID < uint64(total%int(workerCount)) {
		target++
	}
	return target, nil
}

// Tick releases one exact global per-second grant and streams every transient
// intent to emit. Emitter failure is returned immediately and never hidden.
func (g *TrafficGenerator) Tick(demand []uint64, emit func(TrafficIntent) error) (TrafficTickSnapshot, error) {
	if emit == nil {
		return TrafficTickSnapshot{}, errTrafficEmitter
	}
	grant, err := g.allocator.Tick(demand)
	if err != nil {
		return TrafficTickSnapshot{}, err
	}
	return g.ApplyGrant(grant.Released[g.workerID], emit)
}

// ApplyGrant streams only this worker's coordinator-apportioned release. It
// deliberately does not advance the generator's local RateAllocator.
func (g *TrafficGenerator) ApplyGrant(released uint64, emit func(TrafficIntent) error) (snapshot TrafficTickSnapshot, resultErr error) {
	if g == nil || emit == nil {
		return TrafficTickSnapshot{}, errTrafficEmitter
	}
	defer func() {
		g.snapshot.PrimaryReleased += snapshot.Released
		g.snapshot.Person += snapshot.Person
		g.snapshot.Group += snapshot.Group
		g.snapshot.PayloadBytes += snapshot.PayloadBytes
	}()
	for count := uint64(0); count < released; count++ {
		intent, err := g.primaryIntent()
		if err != nil {
			return snapshot, err
		}
		if err := emit(intent); err != nil {
			return snapshot, err
		}
		snapshot.Released++
		if intent.Kind == TrafficPerson {
			snapshot.Person++
		} else {
			snapshot.Group++
		}
		payloadIndex, ok := payloadClassIndex(intent.PayloadBytes)
		if !ok {
			return snapshot, errTrafficGeneratorConfig
		}
		snapshot.PayloadCounts[payloadIndex]++
		snapshot.PayloadBytes += uint64(intent.PayloadBytes)
	}
	return snapshot, nil
}

// NextCanary emits at most one due very-large-group probe. Calling it again at
// the same instant cannot duplicate the minute; catch-up remains caller paced.
func (g *TrafficGenerator) NextCanary(now time.Time) (TrafficIntent, bool, error) {
	if g.nextCanary.IsZero() || now.Before(g.nextCanary) {
		return TrafficIntent{}, false, nil
	}
	canary, err := g.catalog.VeryLargeCanary(g.canaryOrdinal)
	if err != nil {
		return TrafficIntent{}, false, err
	}
	group := canary.Group
	workerID, err := g.catalog.GroupOwner(group.Index)
	if err != nil {
		return TrafficIntent{}, false, err
	}
	logicalOrdinal, err := scopedLogicalOrdinal(g.generation, LogicalDomainCanary, g.canaryOrdinal)
	if err != nil {
		return TrafficIntent{}, false, err
	}
	payloadBytes, err := g.model.PayloadSizeFor(g.canaryOrdinal)
	if err != nil {
		return TrafficIntent{}, false, err
	}
	intent := TrafficIntent{
		Logical: LogicalSend{LogicalSend: logicalOrdinal, WorkerID: uint32(workerID), Kind: TrafficGroup}, Kind: TrafficGroup,
		ChannelID: group.ID, GroupCategory: GroupVeryLarge, PayloadBytes: payloadBytes, Canary: true, Domain: LogicalDomainCanary,
	}
	g.canaryOrdinal++
	g.nextCanary = g.nextCanary.Add(canary.Every)
	if workerID != g.workerID {
		return TrafficIntent{}, false, nil
	}
	g.snapshot.Canaries++
	return intent, true, nil
}

// Snapshot returns a constant-size copy.
func (g *TrafficGenerator) Snapshot() TrafficGeneratorSnapshot {
	if g == nil {
		return TrafficGeneratorSnapshot{}
	}
	return g.snapshot
}

// reset discards one run's transient ordinals and credit before a new engine
// generation starts. The immutable identity/model/catalog inputs are reused.
func (g *TrafficGenerator) reset(start time.Time, generation uint64) error {
	if g == nil || start.IsZero() || generation == 0 || generation > maxLogicalGeneration {
		return errTrafficGeneratorConfig
	}
	weights := make([]int64, g.workload.Workers)
	for index := range weights {
		weights[index] = 1
	}
	allocator, err := NewRateAllocator(uint64(g.workload.SendRatePerSecond), uint64(g.workload.MaxGlobalBurst), weights)
	if err != nil {
		return err
	}
	g.allocator = allocator
	g.generation = generation
	g.primaryOrdinal = 0
	g.canaryOrdinal = 0
	g.nextCanary = time.Time{}
	if g.workload.Groups.VeryLarge > 0 {
		g.nextCanary = start.Add(g.workload.Groups.VeryLargeSendEvery)
	}
	g.snapshot = TrafficGeneratorSnapshot{HotSet: g.hotSet}
	return nil
}

func (g *TrafficGenerator) primaryIntent() (TrafficIntent, error) {
	ordinal, err := g.identity.GlobalIndex(g.workerID, g.primaryOrdinal)
	if err != nil || ordinal > maxLogicalOrdinal {
		return TrafficIntent{}, errTrafficGeneratorConfig
	}
	g.primaryOrdinal++
	kind, err := g.model.TrafficFor(ordinal)
	if err != nil {
		return TrafficIntent{}, err
	}
	payloadBytes, err := g.model.PayloadSizeFor(ordinal)
	if err != nil {
		return TrafficIntent{}, err
	}
	intent := TrafficIntent{Kind: kind, PayloadBytes: payloadBytes, Domain: LogicalDomainPrimary}
	domainOrdinal := ordinal
	if kind == TrafficPerson {
		personOrdinal, ordinalErr := exactCycleChoiceOrdinal(ordinal, g.model.trafficPhase, 0, g.model.traffic[:])
		if ordinalErr != nil {
			return TrafficIntent{}, ordinalErr
		}
		direction, err := g.model.DirectionFor(personOrdinal)
		if err != nil {
			return TrafficIntent{}, err
		}
		intent.Direction = direction
	} else {
		groupOrdinal, ordinalErr := exactCycleChoiceOrdinal(ordinal, g.model.trafficPhase, 1, g.model.traffic[:])
		if ordinalErr != nil {
			return TrafficIntent{}, ordinalErr
		}
		domainOrdinal = groupOrdinal
		group, err := g.catalog.PrimaryTargetForWorker(groupOrdinal, g.workerID)
		if err != nil {
			return TrafficIntent{}, err
		}
		intent.ChannelID = group.ID
		intent.GroupCategory = group.Category
	}
	domain := LogicalDomainPrimary
	if kind == TrafficGroup {
		domain = LogicalDomainGroup
		intent.Domain = LogicalDomainGroup
	}
	scopedOrdinal, err := scopedLogicalOrdinal(g.generation, domain, domainOrdinal)
	if err != nil {
		return TrafficIntent{}, err
	}
	intent.Logical = LogicalSend{LogicalSend: scopedOrdinal, WorkerID: uint32(g.workerID), Kind: kind}
	return intent, nil
}

// exactCycleChoiceOrdinal counts matching positions strictly before ordinal.
// It lets independently owned worker partitions reconstruct one global cycle.
func exactCycleChoiceOrdinal(ordinal, phase uint64, choice int, shares []int) (uint64, error) {
	if !validPositiveDistribution(shares) || choice < 0 || choice >= len(shares) {
		return 0, errTrafficDistribution
	}
	result := (ordinal / distributionCycle) * uint64(shares[choice])
	for position := uint64(0); position < ordinal%distributionCycle; position++ {
		selected, err := exactCycleChoice(position, phase, shares)
		if err != nil {
			return 0, err
		}
		if selected == choice {
			result++
		}
	}
	return result, nil
}

func packetForTrafficIntent(logical LogicalSend, payload []byte) *frame.SendPacket {
	channelType := uint8(frame.ChannelTypePerson)
	if logical.Kind == TrafficGroup {
		channelType = frame.ChannelTypeGroup
	}
	return &frame.SendPacket{
		ClientSeq: logical.LogicalSend + 1, ClientMsgNo: logical.ClientMsgNo,
		ChannelID: logical.Target, ChannelType: channelType, Payload: payload,
	}
}

func payloadClassIndex(size int) (int, bool) {
	switch size {
	case 256:
		return 0, true
	case 1_024:
		return 1, true
	case 4_096:
		return 2, true
	case 16_384:
		return 3, true
	default:
		return 0, false
	}
}

// ScheduledRetry is one bounded future heap row.
type ScheduledRetry struct {
	Intent  TrafficIntent
	Attempt RetryAttempt
	Due     time.Time
	index   int
	order   uint64
}

type retryHeap []*ScheduledRetry

func (h retryHeap) Len() int { return len(h) }
func (h retryHeap) Less(i, j int) bool {
	if h[i].Due.Equal(h[j].Due) {
		return h[i].order < h[j].order
	}
	return h[i].Due.Before(h[j].Due)
}
func (h retryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *retryHeap) Push(value any) {
	entry := value.(*ScheduledRetry)
	entry.index = len(*h)
	*h = append(*h, entry)
}
func (h *retryHeap) Pop() any {
	old := *h
	last := len(old) - 1
	entry := old[last]
	old[last] = nil
	entry.index = -1
	*h = old[:last]
	return entry
}

// RetrySchedulerSnapshot contains only bounded heap gauges.
type RetrySchedulerSnapshot struct {
	Depth      int
	Peak       int
	Capacity   int
	Saturation uint64
}

// RetryScheduler retains only attempts waiting for their approved delay.
// One engine goroutine owns it.
type RetryScheduler struct {
	policy     RetryPolicy
	capacity   int
	entries    retryHeap
	byMessage  map[string]*ScheduledRetry
	peak       int
	saturation uint64
	nextOrder  uint64
}

// NewRetryScheduler validates its explicit retained-state capacity.
func NewRetryScheduler(policy RetryPolicy, capacity int) (*RetryScheduler, error) {
	if policy.identity == nil || capacity <= 0 || capacity > maxVerifierCapacity {
		return nil, errRetrySchedulerConfig
	}
	scheduler := &RetryScheduler{policy: policy, capacity: capacity, byMessage: make(map[string]*ScheduledRetry)}
	heap.Init(&scheduler.entries)
	return scheduler, nil
}

// Schedule plans the next attempt and reuses the original logical identity.
func (s *RetryScheduler) Schedule(intent TrafficIntent, completedAttempt uint8, now time.Time) (ScheduledRetry, error) {
	if completedAttempt >= 3 {
		return ScheduledRetry{}, ErrRetryLimitReached
	}
	if _, exists := s.byMessage[intent.Logical.ClientMsgNo]; exists {
		return ScheduledRetry{}, errRetryAlreadyScheduled
	}
	if len(s.entries) >= s.capacity {
		s.saturation++
		return ScheduledRetry{}, &RuntimeError{code: RuntimeFailureRetryQueueSaturated}
	}
	attempt, err := s.policy.Attempt(intent.Logical, completedAttempt+1)
	if err != nil {
		return ScheduledRetry{}, err
	}
	if attempt.Delay > 0 && now.UnixNano() > math.MaxInt64-int64(attempt.Delay) {
		return ScheduledRetry{}, errRetryDelayOverflow
	}
	entry := &ScheduledRetry{Intent: intent, Attempt: attempt, Due: now.Add(attempt.Delay), order: s.nextOrder}
	s.nextOrder++
	heap.Push(&s.entries, entry)
	s.byMessage[intent.Logical.ClientMsgNo] = entry
	if len(s.entries) > s.peak {
		s.peak = len(s.entries)
	}
	return *entry, nil
}

// PopDue removes at most limit due attempts in deadline order.
func (s *RetryScheduler) PopDue(now time.Time, limit int) []ScheduledRetry {
	if s == nil || limit <= 0 {
		return nil
	}
	result := make([]ScheduledRetry, 0, min(limit, len(s.entries)))
	for len(s.entries) > 0 && len(result) < limit && !s.entries[0].Due.After(now) {
		entry := heap.Pop(&s.entries).(*ScheduledRetry)
		delete(s.byMessage, entry.Intent.Logical.ClientMsgNo)
		result = append(result, *entry)
	}
	return result
}

// cancel physically removes one acknowledged logical send in O(log n).
func (s *RetryScheduler) cancel(clientMsgNo string) bool {
	if s == nil {
		return false
	}
	entry := s.byMessage[clientMsgNo]
	if entry == nil {
		return false
	}
	heap.Remove(&s.entries, entry.index)
	delete(s.byMessage, clientMsgNo)
	return true
}

func (s *RetryScheduler) due(now time.Time) bool {
	return s != nil && len(s.entries) > 0 && !s.entries[0].Due.After(now)
}

// Snapshot exposes heap pressure without identities.
func (s *RetryScheduler) Snapshot() RetrySchedulerSnapshot {
	if s == nil {
		return RetrySchedulerSnapshot{}
	}
	return RetrySchedulerSnapshot{Depth: len(s.entries), Peak: s.peak, Capacity: s.capacity, Saturation: s.saturation}
}
