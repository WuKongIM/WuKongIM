package proxy

import (
	"context"
	"strings"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const (
	// Collect paced concurrent callers before spending a fresh barrier. The idle
	// fast path stays immediate; execution and ownership limits stay independent.
	sendPermissionCohortWindow        = 5 * time.Millisecond
	sendPermissionCohortMaxCalls      = 64
	sendPermissionCohortMaxActive     = 64
	sendPermissionCohortMaxOwnedCalls = 1024
	sendPermissionCohortMaxOwnedBytes = 16 << 20
	// Background embedding callers cannot retain a worker indefinitely. Normal
	// SEND callers carry their own deadline, which always wins when earlier.
	sendPermissionCohortMaxExecution = 30 * time.Second
)

// sendPermissionCohort owns only a sealed batch's fresh raw facts, never policy
// decisions or a reusable result. All mutable ownership fields use the Store mu.
type sendPermissionCohort struct {
	// Arrival cutoff is checked under admission, even if timer scheduling lags.
	collectUntil time.Time
	ctx          context.Context
	cancel       context.CancelFunc
	seal         chan struct{}
	done         chan struct{}
	members      []*sendPermissionCohortMember
	inputs       int
	live         int
	sealed       bool
	finished     bool
}

type sendPermissionCohortMember struct {
	// done publishes this member; cohort remains nil while it waits in FIFO.
	done       chan struct{}
	cohort     *sendPermissionCohort
	prev, next *sendPermissionCohortMember
	ctx        context.Context
	reads      []PermissionMetadataRead
	indexes    []int
	results    []PermissionMetadataReadResult
	// Credits cover copied inputs, aligned results, indexes/dedup and strings.
	// Pending cancellation drops them immediately; selected callers keep them
	// until their already-started worker joins. Fixed cohort/context overhead,
	// allocator overhead, borrowed contexts and raw RPC/DB heap are separate.
	bytes    int
	live     bool
	returned bool
	charged  bool
}

// ReadSendPermissionMetadataBatch reads an idle one/two-fact request immediately
// and collects concurrent callers in a bounded short window. Full execution
// retains ready callers in the same owned-data budget. Seal always precedes
// fresh barriers; late arrivals cannot consume an already executing result.
func (s *Store) ReadSendPermissionMetadataBatch(ctx context.Context, reads []PermissionMetadataRead) []PermissionMetadataReadResult {
	fail := func(err error) []PermissionMetadataReadResult {
		return permissionMetadataErrorResults(make([]PermissionMetadataReadResult, len(reads)), err)
	}
	if len(reads) == 0 {
		return nil
	}
	if s == nil || s.cluster == nil || s.db == nil || len(reads) > permissionBatchMaxReads {
		return fail(metadb.ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	cost := 256
	valid := false
	for _, r := range reads {
		if _, err := permissionReadKey(r); err == nil {
			valid = true
		}
		// Reserve before copying or constructing maps. 1024 bytes per input
		// conservatively covers both aligned arrays and shared execution DTOs.
		n := len(r.UID) + len(r.ChannelID)
		if n > sendPermissionCohortMaxOwnedBytes/2 || cost > sendPermissionCohortMaxOwnedBytes-1024-2*n {
			return fail(ErrPermissionBusy)
		}
		cost += 1024 + 2*n
	}
	if !valid {
		return fail(metadb.ErrInvalidArgument)
	}
	s.permissionCohortMu.Lock()
	window := s.permissionCohortWindow
	if window == 0 {
		window = sendPermissionCohortWindow
	}
	if s.permissionCohortClosed {
		s.permissionCohortMu.Unlock()
		return fail(context.Canceled)
	}
	c := s.permissionCollecting
	if c != nil && !time.Now().Before(c.collectUntil) {
		s.sealSendPermissionCohortLocked(c)
		c = nil
	}
	if s.permissionCohortRequests == sendPermissionCohortMaxOwnedCalls || s.permissionCohortBytes+cost > sendPermissionCohortMaxOwnedBytes {
		// Capture every satisfied guard under the same ownership lock. The value
		// must survive later returns without retaining the Store or any identities.
		snapshot := PermissionAdmissionRejection{
			ActiveCohorts: len(s.permissionCohorts), OwnedCalls: s.permissionCohortRequests,
			OwnedBytes: s.permissionCohortBytes, RequestBytes: cost,
		}
		if s.permissionCohortRequests == sendPermissionCohortMaxOwnedCalls {
			snapshot.Reasons |= 2
		}
		if s.permissionCohortBytes+cost > sendPermissionCohortMaxOwnedBytes {
			snapshot.Reasons |= 4
		}
		s.permissionCohortMu.Unlock()
		s.permissionCount("cohort_busy", 1)
		return fail(&permissionAdmissionRejectionError{snapshot: snapshot})
	}
	if c != nil && c.inputs+len(reads) > permissionBatchMaxReads {
		s.sealSendPermissionCohortLocked(c)
		c = nil
	}
	// A new arrival must not bypass older ready members.
	queued := s.permissionPendingHead != nil || c == nil && len(s.permissionCohorts) == sendPermissionCohortMaxActive
	fresh := c == nil && !queued
	// Ordinary sequential SENDs have two distinct mandatory facts. With no
	// concurrent work there is nothing to share, so keep bounded ownership but
	// avoid a timer, worker, copied inputs and result realignment. An explicit
	// test collection window continues to exercise the shared-cohort path.
	if fresh && len(s.permissionCohorts) == 0 && s.permissionCohortWindow == 0 &&
		(len(reads) == 1 || len(reads) == 2 && reads[0] != reads[1]) {
		execution, cancel := context.WithTimeout(ctx, sendPermissionCohortMaxExecution)
		c = &sendPermissionCohort{ctx: execution, cancel: cancel, sealed: true}
		if s.permissionCohorts == nil {
			s.permissionCohorts = make(map[*sendPermissionCohort]struct{})
		}
		s.permissionCohorts[c] = struct{}{}
		s.permissionCohortWG.Add(1)
		s.permissionCohortRequests++
		s.permissionCohortBytes += cost
		s.permissionCohortOwned("cohorts", 1)
		s.permissionCohortOwned("calls", 1)
		s.permissionCohortOwned("budget_bytes", cost)
		s.permissionCohortMu.Unlock()
		defer func() {
			cancel()
			s.permissionCohortMu.Lock()
			delete(s.permissionCohorts, c)
			s.permissionCohortRequests--
			s.permissionCohortBytes -= cost
			s.permissionCohortOwned("cohorts", -1)
			s.permissionCohortOwned("calls", -1)
			s.permissionCohortOwned("budget_bytes", -cost)
			next := s.dispatchSendPermissionReadyLocked()
			s.permissionCohortMu.Unlock()
			s.startSendPermissionCohort(next)
			s.permissionCohortWG.Done()
		}()
		s.permissionCount("cohorts", 1)
		s.permissionCount("cohort_requests", 1)
		s.permissionCount("cohort_facts", len(reads))
		// Borrowed inputs remain caller-owned until this synchronous reader joins.
		// Close cancels this sealed owner and waits on the same ownership group.
		out := s.readSendPermissionMetadataBatch(execution, reads)
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		return out
	}
	if fresh {
		c = s.newSendPermissionCohortLocked(time.Now().Add(window))
		s.permissionCollecting = c
	}
	owned := make([]PermissionMetadataRead, len(reads))
	for i, r := range reads {
		r.UID = strings.Clone(r.UID)
		r.ChannelID = strings.Clone(r.ChannelID)
		owned[i] = r
	}
	m := &sendPermissionCohortMember{ctx: ctx, done: make(chan struct{}), reads: owned, bytes: cost, live: true, charged: true}
	if queued {
		m.prev = s.permissionPendingTail
		if m.prev == nil {
			s.permissionPendingHead = m
		} else {
			m.prev.next = m
		}
		s.permissionPendingTail = m
	} else {
		s.assignSendPermissionMemberLocked(c, m)
	}
	s.permissionCohortRequests++
	s.permissionCohortBytes += cost
	s.permissionCohortOwned("calls", 1)
	s.permissionCohortOwned("budget_bytes", cost)
	if !queued && (len(c.members) == sendPermissionCohortMaxCalls || c.inputs == permissionBatchMaxReads) {
		s.sealSendPermissionCohortLocked(c)
	}
	s.permissionCohortMu.Unlock()
	if fresh {
		s.startSendPermissionCohort(c)
	}
	select {
	case <-m.done:
	case <-ctx.Done():
	}
	s.permissionCohortMu.Lock()
	m.returned = true
	c = m.cohort
	last := false
	if c == nil {
		// Pending work has never borrowed its inputs; drop them before refund.
		if m.charged {
			s.removeSendPermissionPendingLocked(m)
			s.dropSendPermissionPendingLocked(m)
		}
	} else {
		if m.live {
			m.live = false
			c.live--
		}
		last = c.live == 0 && !c.finished
		if last {
			c.cancel()
			s.sealSendPermissionCohortLocked(c)
		}
	}
	results := m.results
	if c != nil && c.finished {
		m.results = nil // transfer to the returning caller before releasing credits
		s.releaseSendPermissionCohortMemberLocked(m)
	}
	s.permissionCohortMu.Unlock()
	// The last canceled member owns the real execution join, even if its
	// provider ignores cancellation. Other live members keep that owner alive.
	if last {
		<-c.done
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if len(results) == 0 {
		return fail(context.Canceled)
	}
	return results
}

// newSendPermissionCohortLocked registers ownership before any launch or Close.
// A zero cutoff is an already-ready batch and never creates a collection timer.
func (s *Store) newSendPermissionCohortLocked(cutoff time.Time) *sendPermissionCohort {
	root, cancel := context.WithCancel(context.Background())
	c := &sendPermissionCohort{ctx: root, cancel: cancel, seal: make(chan struct{}), done: make(chan struct{}), collectUntil: cutoff}
	if s.permissionCohorts == nil {
		s.permissionCohorts = make(map[*sendPermissionCohort]struct{})
	}
	s.permissionCohorts[c] = struct{}{}
	s.permissionCohortWG.Add(1)
	s.permissionCohortOwned("cohorts", 1)
	return c
}

func (s *Store) startSendPermissionCohort(c *sendPermissionCohort) {
	if c == nil {
		return
	}
	goruntimeregistry.SafeGo(goruntimeregistry.Default(), goruntimeregistry.TaskSlotPermissionBatch, func() {
		defer s.permissionCohortWG.Done()
		s.runSendPermissionCohort(c)
	})
}

func (s *Store) assignSendPermissionMemberLocked(c *sendPermissionCohort, m *sendPermissionCohortMember) {
	m.cohort = c
	c.members = append(c.members, m)
	c.inputs += len(m.reads)
	c.live++
}

// removeSendPermissionPendingLocked unlinks a caller without scanning or keeping
// canceled history. The caller still owns its reservation until references drop.
func (s *Store) removeSendPermissionPendingLocked(m *sendPermissionCohortMember) {
	if m.prev == nil {
		s.permissionPendingHead = m.next
	} else {
		m.prev.next = m.next
	}
	if m.next == nil {
		s.permissionPendingTail = m.prev
	} else {
		m.next.prev = m.prev
	}
	m.prev, m.next = nil, nil
}

func (s *Store) dropSendPermissionPendingLocked(m *sendPermissionCohortMember) {
	m.live = false
	m.reads = nil
	s.releaseSendPermissionCohortMemberLocked(m)
	close(m.done)
}

// dispatchSendPermissionReadyLocked fills one newly freed physical owner with
// the FIFO prefix. Ready callers never wait for another collection timer and
// deduplication cannot weaken the original-input limit or skip a large head.
func (s *Store) dispatchSendPermissionReadyLocked() *sendPermissionCohort {
	if s.permissionCohortClosed || len(s.permissionCohorts) >= sendPermissionCohortMaxActive {
		return nil
	}
	var c *sendPermissionCohort
	for m := s.permissionPendingHead; m != nil; m = s.permissionPendingHead {
		if m.ctx.Err() != nil {
			s.removeSendPermissionPendingLocked(m)
			s.dropSendPermissionPendingLocked(m)
			continue
		}
		if c != nil && (len(c.members) == sendPermissionCohortMaxCalls || c.inputs+len(m.reads) > permissionBatchMaxReads) {
			break
		}
		s.removeSendPermissionPendingLocked(m)
		if c == nil {
			c = s.newSendPermissionCohortLocked(time.Time{})
		}
		s.assignSendPermissionMemberLocked(c, m)
	}
	if c != nil {
		s.sealSendPermissionCohortLocked(c)
	}
	return c
}

// sealSendPermissionCohortLocked closes membership before any authority read.
func (s *Store) sealSendPermissionCohortLocked(c *sendPermissionCohort) {
	if c.sealed {
		return
	}
	c.sealed = true
	if s.permissionCollecting == c {
		s.permissionCollecting = nil
	}
	close(c.seal)
}

func (s *Store) runSendPermissionCohort(c *sendPermissionCohort) {
	if !c.collectUntil.IsZero() {
		timer := time.NewTimer(time.Until(c.collectUntil))
		select {
		case <-timer.C:
		case <-c.seal:
		case <-c.ctx.Done():
		}
		timer.Stop()
	}
	s.permissionCohortMu.Lock()
	s.sealSendPermissionCohortLocked(c)
	members := c.members
	deadline := time.Now().Add(sendPermissionCohortMaxExecution)
	latest := time.Time{}
	noDeadline := false
	for _, m := range members {
		if m.ctx.Err() != nil {
			continue
		}
		if d, ok := m.ctx.Deadline(); ok {
			if d.After(latest) {
				latest = d
			}
		} else {
			noDeadline = true
		}
	}
	if !noDeadline && !latest.IsZero() && latest.Before(deadline) {
		deadline = latest
	}
	s.permissionCohortMu.Unlock()
	execution, cancel := context.WithDeadline(c.ctx, deadline)
	defer cancel()
	unique := make([]PermissionMetadataRead, 0, c.inputs)
	indexes := make(map[PermissionMetadataRead]int, c.inputs)
	for _, m := range members {
		if m.ctx.Err() != nil {
			continue
		}
		m.indexes = make([]int, len(m.reads))
		for i, r := range m.reads {
			index, ok := indexes[r]
			if !ok {
				index = len(unique)
				indexes[r] = index
				unique = append(unique, r)
			}
			m.indexes[i] = index
		}
	}
	s.permissionCount("cohorts", 1)
	s.permissionCount("cohort_requests", len(members))
	s.permissionCount("cohort_facts", len(unique))
	// This existing reader retains per-Slot routing, independent failures and
	// the one stale-group retry. No cache or additional RPC retry is introduced.
	facts := s.readSendPermissionMetadataBatch(execution, unique)
	s.permissionCohortMu.Lock()
	for _, m := range members {
		if m.ctx.Err() == nil && len(m.indexes) > 0 {
			m.results = make([]PermissionMetadataReadResult, len(m.indexes))
			for i, index := range m.indexes {
				m.results[i] = facts[index]
			}
		}
		m.reads = nil
		m.indexes = nil
	}
	// The reader is joined. Clear shared DTO aliases before any returned
	// member gives its owned-data reservation back.
	facts, unique, indexes = nil, nil, nil
	for _, m := range members {
		m.ctx = nil
		if m.returned {
			m.results = nil
			s.releaseSendPermissionCohortMemberLocked(m)
		}
		close(m.done)
	}
	c.finished = true
	c.members = nil
	delete(s.permissionCohorts, c)
	s.permissionCohortOwned("cohorts", -1)
	c.cancel()
	close(c.done)
	next := s.dispatchSendPermissionReadyLocked()
	s.permissionCohortMu.Unlock()
	s.startSendPermissionCohort(next)
}

func (s *Store) releaseSendPermissionCohortMemberLocked(m *sendPermissionCohortMember) {
	if !m.charged {
		return
	}
	m.charged = false
	m.ctx = nil
	m.cohort = nil
	s.permissionCohortRequests--
	s.permissionCohortBytes -= m.bytes
	s.permissionCohortOwned("calls", -1)
	s.permissionCohortOwned("budget_bytes", -m.bytes)
}

// CloseSendPermissionReads closes admission, drops pending members, cancels
// every collecting or executing read, and joins workers before their transport/storage dependencies
// close. Repeated calls are safe; a Node restart creates a fresh Store.
func (s *Store) CloseSendPermissionReads() {
	if s == nil {
		return
	}
	s.permissionCohortMu.Lock()
	s.permissionCohortClosed = true
	for m := s.permissionPendingHead; m != nil; m = s.permissionPendingHead {
		s.removeSendPermissionPendingLocked(m)
		s.dropSendPermissionPendingLocked(m)
	}
	for c := range s.permissionCohorts {
		c.cancel()
		s.sealSendPermissionCohortLocked(c)
	}
	s.permissionCohortMu.Unlock()
	s.permissionCohortWG.Wait()
}
