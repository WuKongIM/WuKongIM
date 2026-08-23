package chatlifecycle

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const (
	productionLifecycleInitialLoadSchedulingReserve = time.Second
	productionLifecycleMaxPollEvery                 = 10 * time.Minute
	productionLifecycleFirstLeaseGrace              = 10 * time.Minute
)

// ProductionLifecycleWorker is the fenced control-plane seam required from
// each of the exact three production workers.
type ProductionLifecycleWorker interface {
	LeaseLifecycleCandidates(context.Context, WorkerLifecycleCandidateLeaseRequest) (WorkerLifecycleCandidateLeaseResponse, error)
	ApproveLifecycleReheat(context.Context, WorkerLifecycleReheatRequest) (WorkerLifecycleReheatResponse, error)
}

// ProductionLifecycleProber reads one explicit candidate batch from every
// service node without changing runtime state.
type ProductionLifecycleProber interface {
	ProbeChannelRuntimeAll(context.Context, model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error)
}

// ProductionLifecycleOptions supplies the three fenced workers and the
// bounded polling controls for the long-running production proof loop.
type ProductionLifecycleOptions struct {
	// Enabled starts the formal natural hot/cold/reheat proof. Local throughput
	// diagnostics leave it disabled and make no natural-lifecycle claim.
	Enabled        bool
	Workers        [3]ProductionLifecycleWorker
	Prober         ProductionLifecycleProber
	Clock          ObserverClock
	SlotAssignment LifecycleSlotAssignment
	PollEvery      time.Duration
	ProbeOptions   LifecycleProbeOptions
}

// ProductionLifecycle owns one transient 1,200-candidate proof. Snapshot is
// identity-free and safe to call while Run is active.
type ProductionLifecycle struct {
	options ProductionLifecycleOptions

	runMu   sync.Mutex
	started bool

	mu              sync.Mutex
	active          []*productionLifecycleCohort
	completed       LifecycleProofSnapshot
	harnessFailures uint64
}

type productionLifecycleCohort struct {
	proof      *LifecycleProof
	candidates []LifecycleCandidate
	owner      map[string]uint64
	approved   map[string]bool
}

// NewProductionLifecycle constructs one single-use production proof loop.
func NewProductionLifecycle(options ProductionLifecycleOptions) (*ProductionLifecycle, error) {
	if !options.Enabled {
		return &ProductionLifecycle{
			options:   options,
			completed: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()},
		}, nil
	}
	for _, worker := range options.Workers {
		if worker == nil {
			return nil, ErrLifecycleHarnessInvalid
		}
	}
	if options.Prober == nil || options.SlotAssignment.HashSlotCount() != formalHashSlots {
		return nil, ErrLifecycleHarnessInvalid
	}
	if options.Clock == nil {
		options.Clock = realObserverClock{}
	}
	if options.PollEvery == 0 {
		options.PollEvery = time.Second
	}
	if options.PollEvery < 0 || options.PollEvery > productionLifecycleMaxPollEvery {
		return nil, ErrLifecycleHarnessInvalid
	}
	if options.ProbeOptions.BatchSize == 0 {
		options.ProbeOptions.BatchSize = lifecycleCohortSize
	}
	if options.ProbeOptions.MaxConcurrency == 0 {
		options.ProbeOptions.MaxConcurrency = 1
	}
	if options.ProbeOptions.RequestTimeout == 0 {
		options.ProbeOptions.RequestTimeout = observerMaxRoundTimeout
	}
	if options.ProbeOptions.BatchSize < 1 || options.ProbeOptions.BatchSize > lifecycleMaxProbeBatch ||
		options.ProbeOptions.MaxConcurrency < 1 || options.ProbeOptions.MaxConcurrency > lifecycleMaxProbeParallel ||
		options.ProbeOptions.RequestTimeout < 0 || options.ProbeOptions.RequestTimeout > 30*time.Second {
		return nil, ErrLifecycleHarnessInvalid
	}
	return &ProductionLifecycle{
		options:   options,
		completed: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()},
	}, nil
}

// Run leases one fixed exact 12x100 cohort after five active minutes and polls
// it through natural cold/reheat completion. It returns the original
// cancellation or a closed product/harness classification and always releases
// transient identities.
func (p *ProductionLifecycle) Run(ctx context.Context, fence WorkerFence) error {
	if p == nil || ctx == nil || !validWorkerFence(fence) {
		if p != nil {
			p.recordHarnessFailure()
		}
		return ErrLifecycleHarnessInvalid
	}
	p.runMu.Lock()
	if p.started {
		p.runMu.Unlock()
		p.recordHarnessFailure()
		return ErrLifecycleHarnessInvalid
	}
	p.started = true
	p.runMu.Unlock()
	defer p.releaseAllActive()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !p.options.Enabled {
		<-ctx.Done()
		return ctx.Err()
	}
	startedAt := p.options.Clock.Now()
	if startedAt.IsZero() {
		p.recordHarnessFailure()
		return ErrLifecycleHarnessInvalid
	}
	nextLease := startedAt.Add(lifecycleNaturalQuiet)
	if !nextLease.After(startedAt) {
		p.recordHarnessFailure()
		return ErrLifecycleHarnessInvalid
	}
	ticker := p.options.Clock.NewTicker(p.options.PollEvery)
	if ticker == nil {
		p.recordHarnessFailure()
		return ErrLifecycleHarnessInvalid
	}
	defer ticker.Stop()
	lastTick := startedAt
	leasedOnce := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case tick, ok := <-ticker.C():
			if !ok || tick.IsZero() || !tick.After(lastTick) {
				p.recordHarnessFailure()
				return ErrLifecycleHarnessInvalid
			}
			lastTick = tick
			leaseDue := !leasedOnce && !tick.Before(nextLease)
			if leaseDue {
				if !tick.Before(nextLease.Add(productionLifecycleFirstLeaseGrace)) {
					p.recordHarnessFailure()
					return ErrLifecycleHarnessInvalid
				}
				cohort, leaseErr := p.leaseCohort(ctx, fence, tick)
				if leaseErr != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					p.recordHarnessFailure()
					return ErrLifecycleHarnessInvalid
				}
				p.mu.Lock()
				p.active = append(p.active, cohort)
				p.mu.Unlock()
				leasedOnce = true
			}
			pollErr := p.pollRound(ctx, fence, tick)
			if pollErr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return pollErr
			}
			p.releaseCompleted()
		}
	}
}

func (p *ProductionLifecycle) leaseCohort(ctx context.Context, fence WorkerFence, now time.Time) (*productionLifecycleCohort, error) {
	initialLoadDeadline := now.Add(2*p.options.ProbeOptions.RequestTimeout + productionLifecycleInitialLoadSchedulingReserve)
	if !initialLoadDeadline.After(now) {
		return nil, ErrLifecycleHarnessInvalid
	}
	type leaseOutcome struct {
		response WorkerLifecycleCandidateLeaseResponse
		err      error
	}
	outcomes := make([]leaseOutcome, len(p.options.Workers))
	roundCtx, cancel := context.WithTimeout(ctx, p.options.ProbeOptions.RequestTimeout)
	defer cancel()
	var wait sync.WaitGroup
	for workerID, worker := range p.options.Workers {
		wait.Add(1)
		go func(workerID int, worker ProductionLifecycleWorker) {
			defer wait.Done()
			outcomes[workerID].response, outcomes[workerID].err = worker.LeaseLifecycleCandidates(roundCtx, WorkerLifecycleCandidateLeaseRequest{
				WorkerFence: fence, Requested: lifecycleCohortSize, InitialLoadDeadline: initialLoadDeadline,
			})
		}(workerID, worker)
	}
	wait.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	p.mu.Lock()
	activeIdentities := make(map[string]struct{}, len(p.active)*lifecycleCohortSize)
	for _, cohort := range p.active {
		for _, candidate := range cohort.candidates {
			activeIdentities[candidate.ChannelID] = struct{}{}
		}
	}
	p.mu.Unlock()
	all := make([]LifecycleCandidate, 0, len(p.options.Workers)*lifecycleCohortSize)
	owner := make(map[string]uint64, lifecycleCohortSize)
	for workerID, outcome := range outcomes {
		response := outcome.response
		if outcome.err != nil || !sameWorkerFence(response.WorkerFence, fence) || response.WorkerID != uint64(workerID) ||
			response.WorkerCount != uint64(len(p.options.Workers)) || len(response.Candidates) > lifecycleCohortSize {
			return nil, ErrLifecycleHarnessInvalid
		}
		for _, candidate := range response.Candidates {
			if _, duplicate := owner[candidate.ChannelID]; duplicate {
				return nil, ErrLifecycleHarnessInvalid
			}
			owner[candidate.ChannelID] = uint64(workerID)
			if _, active := activeIdentities[candidate.ChannelID]; active {
				continue
			}
			all = append(all, candidate)
		}
	}
	selected, err := SelectLifecycleCohort(all, initialLoadDeadline, p.options.SlotAssignment, formalLogicalSlotGroups)
	if err != nil {
		return nil, ErrLifecycleHarnessInvalid
	}
	proof, err := NewLifecycleProof(selected)
	if err != nil {
		return nil, ErrLifecycleHarnessInvalid
	}
	selectedOwners := make(map[string]uint64, len(selected))
	for _, candidate := range selected {
		workerID, exists := owner[candidate.ChannelID]
		if !exists {
			return nil, ErrLifecycleHarnessInvalid
		}
		selectedOwners[candidate.ChannelID] = workerID
	}
	return &productionLifecycleCohort{
		proof: proof, candidates: append([]LifecycleCandidate(nil), selected...),
		owner: selectedOwners, approved: make(map[string]bool, len(selected)),
	}, nil
}

func (p *ProductionLifecycle) pollRound(ctx context.Context, fence WorkerFence, now time.Time) error {
	p.mu.Lock()
	cohorts := append([]*productionLifecycleCohort(nil), p.active...)
	p.mu.Unlock()
	return p.pollCohorts(ctx, fence, now, cohorts)
}

func (p *ProductionLifecycle) pollCohorts(ctx context.Context, fence WorkerFence, now time.Time, cohorts []*productionLifecycleCohort) error {
	pollErrors := make([]error, len(cohorts))
	var wait sync.WaitGroup
	for index, cohort := range cohorts {
		wait.Add(1)
		go func(index int, cohort *productionLifecycleCohort) {
			defer wait.Done()
			_, pollErrors[index] = cohort.proof.Poll(ctx, p.options.Prober, now, p.options.ProbeOptions)
		}(index, cohort)
	}
	wait.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	reheatErrors := p.reheatColdCandidates(ctx, fence, now, cohorts)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var productErr error
	harnessFailed := false
	for _, err := range append(pollErrors, reheatErrors...) {
		if err == nil {
			continue
		}
		if errors.Is(err, ErrLifecycleProductFailure) {
			if productErr == nil {
				productErr = err
			}
			continue
		}
		harnessFailed = true
	}
	if productErr != nil {
		return productErr
	}
	if harnessFailed {
		p.recordHarnessFailure()
		return ErrLifecycleHarnessInvalid
	}
	return nil
}

type productionLifecycleReheatJob struct {
	cohort   *productionLifecycleCohort
	identity string
	index    int
}

func (p *ProductionLifecycle) reheatColdCandidates(ctx context.Context, fence WorkerFence, now time.Time, cohorts []*productionLifecycleCohort) []error {
	jobsByWorker := make([][]productionLifecycleReheatJob, len(p.options.Workers))
	jobCount := 0
	for _, cohort := range cohorts {
		for _, candidate := range cohort.candidates {
			if cohort.approved[candidate.ChannelID] || !cohort.proof.ColdEligible(candidate.ChannelID) {
				continue
			}
			owner, exists := cohort.owner[candidate.ChannelID]
			if !exists || owner >= uint64(len(p.options.Workers)) {
				return []error{ErrLifecycleHarnessInvalid}
			}
			jobsByWorker[owner] = append(jobsByWorker[owner], productionLifecycleReheatJob{
				cohort: cohort, identity: candidate.ChannelID, index: jobCount,
			})
			jobCount++
		}
	}
	results := make([]error, jobCount)
	succeeded := make([]bool, jobCount)
	var wait sync.WaitGroup
	for workerID, jobs := range jobsByWorker {
		if len(jobs) == 0 {
			continue
		}
		wait.Add(1)
		go func(workerID uint64, jobs []productionLifecycleReheatJob) {
			defer wait.Done()
			sender := productionLifecycleOwnedSender{client: p.options.Workers[workerID], fence: fence, workerID: workerID}
			for _, job := range jobs {
				err := job.cohort.proof.Reheat(ctx, now, job.identity, &sender)
				results[job.index] = err
				succeeded[job.index] = err == nil
			}
		}(uint64(workerID), jobs)
	}
	wait.Wait()
	for _, jobs := range jobsByWorker {
		for _, job := range jobs {
			if succeeded[job.index] {
				job.cohort.approved[job.identity] = true
			}
		}
	}
	return results
}

type productionLifecycleOwnedSender struct {
	client   ProductionLifecycleWorker
	fence    WorkerFence
	workerID uint64
}

func (s *productionLifecycleOwnedSender) ApproveLifecycleReheat(ctx context.Context, candidate LifecycleCandidate) error {
	response, err := s.client.ApproveLifecycleReheat(ctx, WorkerLifecycleReheatRequest{
		WorkerFence: s.fence, ChannelID: candidate.ChannelID,
		TimerToken: candidate.TimerToken, ActivityVersion: candidate.ActivityVersion,
	})
	if err != nil {
		return err
	}
	if !sameWorkerFence(response.WorkerFence, s.fence) || response.WorkerID != s.workerID || response.WorkerCount != 3 || !response.Approved {
		return ErrLifecycleHarnessInvalid
	}
	return nil
}

func (p *ProductionLifecycle) releaseCompleted() {
	p.mu.Lock()
	defer p.mu.Unlock()
	retained := p.active[:0]
	for _, cohort := range p.active {
		snapshot := cohort.proof.Snapshot()
		if snapshot.Completed != snapshot.Candidates {
			retained = append(retained, cohort)
			continue
		}
		mergeProductionLifecycleSnapshot(&p.completed, snapshot)
	}
	for index := len(retained); index < len(p.active); index++ {
		p.active[index] = nil
	}
	p.active = retained
}

func (p *ProductionLifecycle) releaseAllActive() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, cohort := range p.active {
		mergeProductionLifecycleSnapshot(&p.completed, cohort.proof.Snapshot())
	}
	for index := range p.active {
		p.active[index] = nil
	}
	p.active = nil
}

func (p *ProductionLifecycle) recordHarnessFailure() {
	p.mu.Lock()
	p.harnessFailures = saturatingIncrement(p.harnessFailures)
	p.mu.Unlock()
}

// Snapshot returns the current identity-free aggregate across completed and
// active cohorts. No candidate or worker identity crosses this boundary.
func (p *ProductionLifecycle) Snapshot() LifecycleProofSnapshot {
	if p == nil {
		return LifecycleProofSnapshot{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	total := p.completed
	total.HarnessFailures = saturatingAdd(total.HarnessFailures, p.harnessFailures)
	for _, cohort := range p.active {
		mergeProductionLifecycleSnapshot(&total, cohort.proof.Snapshot())
	}
	return total
}

func mergeProductionLifecycleSnapshot(total *LifecycleProofSnapshot, value LifecycleProofSnapshot) {
	if total.ReheatLatency.BucketUpper == [16]uint64{} {
		total.ReheatLatency = newWorkerHistogramSnapshot()
	}
	total.Candidates = saturatingAdd(total.Candidates, value.Candidates)
	total.Loaded = saturatingAdd(total.Loaded, value.Loaded)
	total.ColdEligible = saturatingAdd(total.ColdEligible, value.ColdEligible)
	total.Reheated = saturatingAdd(total.Reheated, value.Reheated)
	total.Completed = saturatingAdd(total.Completed, value.Completed)
	total.ProductFailures = saturatingAdd(total.ProductFailures, value.ProductFailures)
	total.HarnessFailures = saturatingAdd(total.HarnessFailures, value.HarnessFailures)
	total.ProductFailureReasons.InitialLoad = saturatingAdd(total.ProductFailureReasons.InitialLoad, value.ProductFailureReasons.InitialLoad)
	total.ProductFailureReasons.RuntimeState = saturatingAdd(total.ProductFailureReasons.RuntimeState, value.ProductFailureReasons.RuntimeState)
	total.ProductFailureReasons.RoleDisagreement = saturatingAdd(total.ProductFailureReasons.RoleDisagreement, value.ProductFailureReasons.RoleDisagreement)
	total.ProductFailureReasons.WatermarkRegression = saturatingAdd(total.ProductFailureReasons.WatermarkRegression, value.ProductFailureReasons.WatermarkRegression)
	total.ProductFailureReasons.ContinuedLoading = saturatingAdd(total.ProductFailureReasons.ContinuedLoading, value.ProductFailureReasons.ContinuedLoading)
	total.ProductFailureReasons.PrematureAbsence = saturatingAdd(total.ProductFailureReasons.PrematureAbsence, value.ProductFailureReasons.PrematureAbsence)
	total.ProductFailureReasons.ReheatTimeout = saturatingAdd(total.ProductFailureReasons.ReheatTimeout, value.ProductFailureReasons.ReheatTimeout)
	total.ProductFailureReasons.PartialReheat = saturatingAdd(total.ProductFailureReasons.PartialReheat, value.ProductFailureReasons.PartialReheat)
	total.ProductFailureReasons.SequenceProof = saturatingAdd(total.ProductFailureReasons.SequenceProof, value.ProductFailureReasons.SequenceProof)
	total.ProductFailureReasons.UnexpectedReload = saturatingAdd(total.ProductFailureReasons.UnexpectedReload, value.ProductFailureReasons.UnexpectedReload)
	total.ProductFailureReasons.ControlTransition = saturatingAdd(total.ProductFailureReasons.ControlTransition, value.ProductFailureReasons.ControlTransition)
	if total.ReheatLatency.BucketUpper != value.ReheatLatency.BucketUpper {
		total.HarnessFailures = saturatingIncrement(total.HarnessFailures)
		return
	}
	total.ReheatLatency.Count = saturatingAdd(total.ReheatLatency.Count, value.ReheatLatency.Count)
	total.ReheatLatency.SumNanos = saturatingAdd(total.ReheatLatency.SumNanos, value.ReheatLatency.SumNanos)
	if value.ReheatLatency.MaxNanos > total.ReheatLatency.MaxNanos {
		total.ReheatLatency.MaxNanos = value.ReheatLatency.MaxNanos
	}
	for index := range total.ReheatLatency.Buckets {
		total.ReheatLatency.Buckets[index] = saturatingAdd(total.ReheatLatency.Buckets[index], value.ReheatLatency.Buckets[index])
	}
}
