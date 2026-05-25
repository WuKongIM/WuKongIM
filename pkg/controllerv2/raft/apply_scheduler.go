package raft

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
	"go.etcd.io/raft/v3/raftpb"
)

type batchApplier interface {
	ApplyBatch(context.Context, []fsm.AppliedCommand) (fsm.BatchApplyResult, error)
}

type appliedMarker interface {
	MarkAppliedBatch(context.Context, uint64) error
}

type applyCompletion func(index uint64, err error)

type applySchedulerConfig struct {
	MaxEntries int
	MaxBytes   uint64
	MaxDelay   time.Duration
}

type toApply struct {
	entries     []raftpb.Entry
	snapshot    raftpb.Snapshot
	confChangeC chan confChangeRequest
}

type confChangeRequest struct {
	entry raftpb.Entry
	resp  chan confChangeResult
}

type confChangeResult struct {
	state raftpb.ConfState
	err   error
}

type applyScheduler struct {
	cfg       applySchedulerConfig
	applier   batchApplier
	marker    appliedMarker
	complete  applyCompletion
	onApplied func(context.Context, uint64) error

	ctx    context.Context
	cancel context.CancelFunc
	jobs   chan toApply
	done   chan struct{}
	errMu  sync.Mutex
	err    error
}

func newApplyScheduler(cfg applySchedulerConfig, applier batchApplier, marker appliedMarker, complete applyCompletion) *applyScheduler {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 128
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 4 << 20
	}
	if cfg.MaxDelay == 0 {
		cfg.MaxDelay = 2 * time.Millisecond
	}
	return &applyScheduler{cfg: cfg, applier: applier, marker: marker, complete: complete, jobs: make(chan toApply, 1024), done: make(chan struct{})}
}

func (s *applyScheduler) start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	go s.run()
}

func (s *applyScheduler) stop() error {
	if s.cancel != nil {
		s.cancel()
	}
	<-s.done
	return s.currentError()
}

func (s *applyScheduler) enqueue(ctx context.Context, job toApply) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case s.jobs <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ctx.Done():
		return s.currentError()
	}
}

func (s *applyScheduler) run() {
	defer close(s.done)
	for {
		select {
		case <-s.ctx.Done():
			return
		case job := <-s.jobs:
			if err := s.applyJob(s.ctx, job); err != nil {
				s.setError(err)
				s.cancel()
				return
			}
		}
	}
}

func (s *applyScheduler) applyJob(ctx context.Context, job toApply) error {
	if len(job.snapshot.Data) > 0 || job.snapshot.Metadata.Index > 0 {
		if err := s.marker.MarkAppliedBatch(ctx, job.snapshot.Metadata.Index); err != nil {
			return err
		}
		if err := s.notifyApplied(ctx, job.snapshot.Metadata.Index); err != nil {
			return err
		}
	}
	return s.applyEntries(ctx, job.entries, job.confChangeC)
}

func (s *applyScheduler) applyEntries(ctx context.Context, entries []raftpb.Entry, confChangeC chan confChangeRequest) error {
	batch := make([]fsm.AppliedCommand, 0, s.cfg.MaxEntries)
	indexes := make([]uint64, 0, s.cfg.MaxEntries)
	var batchBytes uint64
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		result, err := s.applier.ApplyBatch(ctx, batch)
		if err != nil {
			return err
		}
		if len(result.Results) != len(batch) {
			return fmt.Errorf("controllerv2/raft: apply result count %d does not match command count %d", len(result.Results), len(batch))
		}
		last := indexes[len(indexes)-1]
		if err := s.marker.MarkAppliedBatch(ctx, last); err != nil {
			return err
		}
		if err := s.notifyApplied(ctx, last); err != nil {
			return err
		}
		for i, applyResult := range result.Results {
			var proposalErr error
			if applyResult.Rejected {
				proposalErr = ProposalRejectedError{Index: indexes[i], Reason: applyResult.Reason}
			}
			if s.complete != nil {
				s.complete(indexes[i], proposalErr)
			}
		}
		batch = batch[:0]
		indexes = indexes[:0]
		batchBytes = 0
		return nil
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch entry.Type {
		case raftpb.EntryNormal:
			if len(entry.Data) == 0 {
				if err := flush(); err != nil {
					return err
				}
				if err := s.marker.MarkAppliedBatch(ctx, entry.Index); err != nil {
					return err
				}
				if err := s.notifyApplied(ctx, entry.Index); err != nil {
					return err
				}
				if s.complete != nil {
					s.complete(entry.Index, nil)
				}
				continue
			}
			cmd, err := command.Decode(entry.Data)
			if err != nil {
				return err
			}
			if len(batch) >= s.cfg.MaxEntries || (len(batch) > 0 && batchBytes+uint64(len(entry.Data)) > s.cfg.MaxBytes) {
				if err := flush(); err != nil {
					return err
				}
			}
			batch = append(batch, fsm.AppliedCommand{Index: entry.Index, Term: entry.Term, Command: cmd})
			indexes = append(indexes, entry.Index)
			batchBytes += uint64(len(entry.Data))
		case raftpb.EntryConfChange, raftpb.EntryConfChangeV2:
			if err := flush(); err != nil {
				return err
			}
			if confChangeC != nil {
				req := confChangeRequest{entry: entry, resp: make(chan confChangeResult, 1)}
				select {
				case confChangeC <- req:
				case <-ctx.Done():
					return ctx.Err()
				}
				select {
				case result := <-req.resp:
					if result.err != nil {
						return result.err
					}
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if err := s.marker.MarkAppliedBatch(ctx, entry.Index); err != nil {
				return err
			}
			if err := s.notifyApplied(ctx, entry.Index); err != nil {
				return err
			}
		}
	}
	return flush()
}

func (s *applyScheduler) notifyApplied(ctx context.Context, index uint64) error {
	if s.onApplied == nil || index == 0 {
		return nil
	}
	return s.onApplied(ctx, index)
}

func (s *applyScheduler) setError(err error) {
	s.errMu.Lock()
	s.err = err
	s.errMu.Unlock()
}

func (s *applyScheduler) currentError() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.err != nil {
		return s.err
	}
	return ErrStopped
}
