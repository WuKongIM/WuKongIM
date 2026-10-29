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
		// Apply failure must wake pending reads even if there is no next Ready.
		select {
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
			if err := rawNode.Step(msg); err != nil && !errors.Is(err, etcdraft.ErrStepLocalMsg) {
				failAll(err)
				s.setRunError(err)
				return
			}
			s.updateStatus(rawNode, nil)
			failOnLeaderLoss()
		case req := <-proposalCh:
			if err := req.ctx.Err(); err != nil {
				req.resp <- proposalResponse{err: err}
				continue
			}
			if rawNode.Status().RaftState != etcdraft.StateLeader {
				req.resp <- proposalResponse{err: ErrNotLeader}
				continue
			}
			if req.confChange != nil {
				trackerMu.Lock()
				if membershipInFlight {
					trackerMu.Unlock()
					req.resp <- proposalResponse{err: ErrMembershipChangePending}
					continue
				}
				membershipInFlight = true
				trackerMu.Unlock()
				if err := rawNode.ProposeConfChange(*req.confChange); err != nil {
					trackerMu.Lock()
					membershipInFlight = false
					trackerMu.Unlock()
					req.resp <- proposalResponse{err: err}
					continue
				}
				trackerMu.Lock()
				tracker.enqueue(trackedProposal{resp: req.resp, confChange: true})
				trackerMu.Unlock()
				continue
			}
			var data []byte
			if !req.probe {
				encoded, err := command.Encode(req.cmd)
				if err != nil {
					req.resp <- proposalResponse{err: err}
					continue
				}
				data = encoded
			}
			// Probe proposals intentionally carry no Controller command. The empty
			// normal entry advances Raft applied metadata without changing Revision.
			if err := rawNode.Propose(data); err != nil {
				req.resp <- proposalResponse{err: err}
				continue
			}
			trackerMu.Lock()
			tracker.enqueue(trackedProposal{resp: req.resp, probe: req.probe})
			trackerMu.Unlock()
		case req := <-compactCh:
			result, err := s.compactLogNow(req.ctx, store, LogCompactionTriggerManual)
			req.resp <- compactResponse{result: result, err: err}
		}
	}
}
