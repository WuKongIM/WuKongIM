package proxy

import (
	"context"
	"strings"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const (
	sendPermissionCohortWindow        = time.Millisecond
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
	ctx     context.Context
	reads   []PermissionMetadataRead
	indexes []int
	results []PermissionMetadataReadResult
	// Credits cover copied inputs, aligned results, indexes/dedup and strings.
	// A canceled caller keeps them until its already-started worker has joined.
	bytes    int
	live     bool
	returned bool
	charged  bool
}

// ReadSendPermissionMetadataBatch reads an idle one/two-fact request immediately
// and collects concurrent callers in a bounded short window. Seal always precedes
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
	if s.permissionCohortRequests == sendPermissionCohortMaxOwnedCalls || s.permissionCohortBytes+cost > sendPermissionCohortMaxOwnedBytes ||
		((c == nil || c.inputs+len(reads) > permissionBatchMaxReads) && len(s.permissionCohorts) == sendPermissionCohortMaxActive) {
		s.permissionCohortMu.Unlock()
		s.permissionCount("cohort_busy", 1)
		return fail(ErrPermissionBusy)
	}
	if c != nil && c.inputs+len(reads) > permissionBatchMaxReads {
		s.sealSendPermissionCohortLocked(c)
		c = nil
	}
	fresh := c == nil
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
			s.permissionCohortMu.Unlock()
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
		root, cancel := context.WithCancel(context.Background())
		c = &sendPermissionCohort{ctx: root, cancel: cancel, seal: make(chan struct{}), done: make(chan struct{}), collectUntil: time.Now().Add(window)}
		if s.permissionCohorts == nil {
			s.permissionCohorts = make(map[*sendPermissionCohort]struct{})
		}
		s.permissionCohorts[c] = struct{}{}
		s.permissionCollecting = c
		s.permissionCohortWG.Add(1)
		s.permissionCohortOwned("cohorts", 1)
	}
	owned := make([]PermissionMetadataRead, len(reads))
	for i, r := range reads {
		r.UID = strings.Clone(r.UID)
		r.ChannelID = strings.Clone(r.ChannelID)
		owned[i] = r
	}
	m := &sendPermissionCohortMember{ctx: ctx, reads: owned, bytes: cost, live: true, charged: true}
	c.members = append(c.members, m)
	c.inputs += len(reads)
	c.live++
	s.permissionCohortRequests++
	s.permissionCohortBytes += cost
	s.permissionCohortOwned("calls", 1)
	s.permissionCohortOwned("budget_bytes", cost)
	if len(c.members) == sendPermissionCohortMaxCalls || c.inputs == permissionBatchMaxReads {
		s.sealSendPermissionCohortLocked(c)
	}
	s.permissionCohortMu.Unlock()
	if fresh {
		goruntimeregistry.SafeGo(goruntimeregistry.Default(), goruntimeregistry.TaskSlotPermissionBatch, func() {
			defer s.permissionCohortWG.Done()
			s.runSendPermissionCohort(c)
		})
	}
	select {
	case <-c.done:
	case <-ctx.Done():
	}
	s.permissionCohortMu.Lock()
	m.returned = true
	if m.live {
		m.live = false
		c.live--
	}
	last := c.live == 0 && !c.finished
	if last {
		c.cancel()
		s.sealSendPermissionCohortLocked(c)
	}
	if c.finished {
		s.releaseSendPermissionCohortMemberLocked(m)
	}
	s.permissionCohortMu.Unlock()
	// The final cancellation owns the join. Other callers may return promptly
	// while the still-live members keep the shared read and its credits alive.
	if last {
		<-c.done
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if len(m.results) == 0 {
		return fail(context.Canceled)
	}
	return m.results
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
	timer := time.NewTimer(time.Until(c.collectUntil))
	select {
	case <-timer.C:
	case <-c.seal:
	case <-c.ctx.Done():
	}
	timer.Stop()
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
		if m.returned {
			s.releaseSendPermissionCohortMemberLocked(m)
		}
	}
	c.finished = true
	c.members = nil
	delete(s.permissionCohorts, c)
	s.permissionCohortOwned("cohorts", -1)
	c.cancel()
	close(c.done)
	s.permissionCohortMu.Unlock()
}

func (s *Store) releaseSendPermissionCohortMemberLocked(m *sendPermissionCohortMember) {
	if !m.charged {
		return
	}
	m.charged = false
	s.permissionCohortRequests--
	s.permissionCohortBytes -= m.bytes
	s.permissionCohortOwned("calls", -1)
	s.permissionCohortOwned("budget_bytes", -m.bytes)
}

// CloseSendPermissionReads closes cohort admission, cancels every collecting or
// executing read, and joins workers before their transport/storage dependencies
// close. Repeated calls are safe; a Node restart creates a fresh Store.
func (s *Store) CloseSendPermissionReads() {
	if s == nil {
		return
	}
	s.permissionCohortMu.Lock()
	s.permissionCohortClosed = true
	for c := range s.permissionCohorts {
		c.cancel()
		s.sealSendPermissionCohortLocked(c)
	}
	s.permissionCohortMu.Unlock()
	s.permissionCohortWG.Wait()
}
