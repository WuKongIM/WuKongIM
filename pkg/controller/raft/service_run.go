package raft

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/raft/raftstore"
	etcdraft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// Homogeneous input batching bounds a RawNode turn without a new queue or timer.
// The final already-received payload may cross the byte cut once; this is
// fairness budgeting, not a new per-payload admission or hard memory limit.
const (
	maxReadyInputAdmissions = 128
	maxReadyInputBytes      = uint64(4 << 20)
)

func (s *Service) run(store *raftstore.Store, startup runStartupState, stopCh <-chan struct{}, doneCh chan struct{}, stepCh <-chan raftpb.Message, proposalCh <-chan proposalRequest, compactCh <-chan compactRequest, readRequests <-chan *readBarrierRequest, readWake chan struct{}, initCh chan<- error) {
	defer close(doneCh)
	defer store.Close()
	rawNode, err := s.newRawNode(store, startup)
	if err != nil {
		initCh <- err
		return
	}
	if shouldBootstrap(startup) && s.cfg.AllowBootstrap && isSmallestPeer(s.cfg.NodeID, s.cfg.Peers) {
		// Even a single local voter bootstraps through Raft so the first
		// cluster-state file is produced by the same committed-log path.
		if err := rawNode.Bootstrap(raftPeers(s.cfg.Peers)); err != nil {
			initCh <- err
			return
		}
	}

	reads := newReadBarrierTracker()
	defer reads.fail(ErrStopped)
	var durableApplied atomic.Uint64
	durableApplied.Store(startup.AppliedIndex)
	tracker := newProposalTracker()
	var trackerMu sync.Mutex
	membershipInFlight := false
	complete := func(index uint64, result ProposalResult, err error) {
		trackerMu.Lock()
		defer trackerMu.Unlock()
		tracker.complete(index, result, err)
	}
	completeMembership := func(index uint64, result MembershipChangeResult, err error) {
		trackerMu.Lock()
		defer trackerMu.Unlock()
		tracker.completeMembership(index, result, err)
		membershipInFlight = false
	}
	scheduler := newApplyScheduler(applySchedulerConfig{MaxEntries: s.cfg.MaxApplyBatchEntries, MaxBytes: s.cfg.MaxApplyBatchBytes, MaxDelay: s.cfg.MaxApplyDelay}, s.cfg.StateMachine, store, complete)
	scheduler.completeMembership = completeMembership
	scheduler.onApplied = func(ctx context.Context, index uint64) error {
		if err := s.maybeSnapshot(ctx, store, index); err != nil {
			return err
		}
		// This callback runs only after FSM/save and the applied marker both succeed.
		durableApplied.Store(index)
		select {
		case readWake <- struct{}{}:
		default:
		}
		return nil
	}
	if s.cfg.TaskTransitionObserver != nil {
		scheduler.onTaskTransitions = s.cfg.TaskTransitionObserver.ObserveControllerTaskTransitions
	}
	scheduler.start(context.Background())
	defer scheduler.stop()

	s.updateStatus(rawNode, nil)
	initCh <- nil

	ticker := time.NewTicker(s.cfg.TickInterval)
	defer ticker.Stop()

	failAll := func(err error) {
		reads.fail(err)
		trackerMu.Lock()
		tracker.failAll(err)
		membershipInFlight = false
		trackerMu.Unlock()
	}
	failOnLeaderLoss := func() {
		if rawNode.Status().RaftState == etcdraft.StateLeader {
			return
		}
		// Retire reads before processReady can wait on an unrelated blocked apply.
		reads.fail(ErrNotLeader)
		trackerMu.Lock()
		tracker.failAll(ErrNotLeader)
		membershipInFlight = false
		trackerMu.Unlock()
	}
	// admitProposal is called only by this RawNode owner. Consecutive local
	// callers may share a Ready, but Step/Tick retain their original Ready
	// boundary so foreign entries and election no-ops cannot take their futures.
	admitProposal := func(req proposalRequest) uint64 {
		if err := req.ctx.Err(); err != nil {
			req.resp <- proposalResponse{err: err}
			return 0
		}
		if rawNode.Status().RaftState != etcdraft.StateLeader {
			req.resp <- proposalResponse{err: ErrNotLeader}
			return 0
		}
		if req.confChange != nil {
			trackerMu.Lock()
			if membershipInFlight {
				trackerMu.Unlock()
				req.resp <- proposalResponse{err: ErrMembershipChangePending}
				return 0
			}
			membershipInFlight = true
			trackerMu.Unlock()
			if err := rawNode.ProposeConfChange(*req.confChange); err != nil {
				trackerMu.Lock()
				membershipInFlight = false
				trackerMu.Unlock()
				req.resp <- proposalResponse{err: err}
				return uint64(req.confChange.Size())
			}
			trackerMu.Lock()
			tracker.enqueue(trackedProposal{resp: req.resp, confChange: true})
			trackerMu.Unlock()
			return uint64(req.confChange.Size())
		}
		var data []byte
		if !req.probe {
			encoded, err := command.Encode(req.cmd)
			if err != nil {
				req.resp <- proposalResponse{err: err}
				return 0
			}
			data = encoded
		}
		// Probe proposals intentionally carry no Controller command. The empty
		// normal entry advances Raft applied metadata without changing Revision.
		if err := rawNode.Propose(data); err != nil {
			req.resp <- proposalResponse{err: err}
			return uint64(len(data))
		}
		trackerMu.Lock()
		tracker.enqueue(trackedProposal{resp: req.resp, probe: req.probe})
		trackerMu.Unlock()
		return uint64(len(data))
	}
	// Step batches never admit local proposals. Bind every peer/election entry
	// in the following Ready before a later local future can enter the tracker.
	step := func(msg raftpb.Message) (uint64, error) {
		bytes, err := stepRaftInput(rawNode, msg)
		if err != nil && !errors.Is(err, etcdraft.ErrStepLocalMsg) {
			return bytes, err
		}
		s.updateStatus(rawNode, nil)
		failOnLeaderLoss()
		return bytes, nil
	}
	processReady := func() error {
		for rawNode.HasReady() {
			ready := rawNode.Ready()
			isLeader := rawNode.Status().RaftState == etcdraft.StateLeader
			if isLeader {
				s.sendReadyMessages(ready.Messages)
			}
			if err := store.SaveReady(context.Background(), ready.HardState, ready.Entries, ready.Snapshot); err != nil {
				failAll(err)
				return err
			}
			trackerMu.Lock()
			bindResult := tracker.bindAppended(ready.Entries)
			if bindResult.membershipRejected {
				membershipInFlight = false
			}
			trackerMu.Unlock()
			if !isLeader {
				s.sendReadyMessages(ready.Messages)
			}
			job := toApply{entries: ready.CommittedEntries, snapshot: ready.Snapshot}
			confCount := countConfChanges(ready.CommittedEntries)
			if confCount > 0 {
				job.confChangeC = make(chan confChangeRequest, confCount)
			}
			if err := scheduler.enqueueWithStop(context.Background(), job, stopCh); err != nil {
				failAll(err)
				return err
			}
			for i := 0; i < confCount; i++ {
				var req confChangeRequest
				select {
				case req = <-job.confChangeC:
				case <-scheduler.done:
					err := scheduler.currentError()
					failAll(err)
					return err
				case <-stopCh:
					failAll(ErrStopped)
					return ErrStopped
				}
				cs, err := applyConfChange(rawNode, req.entry)
				req.resp <- confChangeResult{state: cs, err: err}
			}
			reads.accept(ready.ReadStates)
			rawNode.Advance(ready)
			s.updateStatus(rawNode, nil)
			failOnLeaderLoss()
		}
		return nil
	}

	for {
		// Check stop/apply failure before the next physical persistence, including
		// when an input burst ended exactly at its fairness budget.
		select {
		case <-stopCh:
			failAll(ErrStopped)
			return
		case <-scheduler.done:
			err := scheduler.currentError()
			failAll(err)
			s.setRunError(err)
			return
		default:
		}
		if err := processReady(); err != nil {
			if !errors.Is(err, ErrStopped) {
				s.setRunError(err)
			}
			return
		}
		select {
		case <-scheduler.done:
			err := scheduler.currentError()
			failAll(err)
			s.setRunError(err)
			return
		default:
		}
		if reads.advance(rawNode, store, durableApplied.Load()) {
			continue
		}
		select {
		case <-scheduler.done:
			err := scheduler.currentError()
			failAll(err)
			s.setRunError(err)
			return
		case <-readWake:
		case req := <-readRequests:
			reads.add(req, rawNode.BasicStatus().Term)
		case <-stopCh:
			failAll(ErrStopped)
			return
		case <-ticker.C:
			rawNode.Tick()
			s.updateStatus(rawNode, nil)
			failOnLeaderLoss()
		case msg := <-stepCh:
			batchBytes, err := step(msg)
			if err != nil {
				failAll(err)
				s.setRunError(err)
				return
			}
		drainSteps:
			for count := 1; count < maxReadyInputAdmissions && batchBytes < maxReadyInputBytes; count++ {
				select {
				case <-stopCh:
					failAll(ErrStopped)
					return
				case <-scheduler.done:
					err := scheduler.currentError()
					failAll(err)
					s.setRunError(err)
					return
				default:
				}
				select {
				case msg := <-stepCh:
					bytes, err := step(msg)
					if err != nil {
						failAll(err)
						s.setRunError(err)
						return
					}
					batchBytes += bytes
				default:
					break drainSteps
				}
			}
		case req := <-proposalCh:
			batchBytes := admitProposal(req)
		drainProposals:
			for count := 1; count < maxReadyInputAdmissions && batchBytes < maxReadyInputBytes; count++ {
				// Do not wait for a batch to fill, and never let a burst defer stop
				// or apply failure until the next persistence round.
				select {
				case <-stopCh:
					failAll(ErrStopped)
					return
				case <-scheduler.done:
					err := scheduler.currentError()
					failAll(err)
					s.setRunError(err)
					return
				default:
				}
				select {
				case req := <-proposalCh:
					batchBytes += admitProposal(req)
				default:
					break drainProposals
				}
			}
		case req := <-compactCh:
			result, err := s.compactLogNow(req.ctx, store, LogCompactionTriggerManual)
			req.resp <- compactResponse{result: result, err: err}
		}
	}
}
