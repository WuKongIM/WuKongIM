package mqttsession

import (
	"cmp"
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// deadlineCandidate owns only bounded identity/cursor fields, never Will bodies.
type deadlineCandidate struct {
	at        int64
	owner     contract.Owner
	cursor    meta.MQTTReadCursor
	reconcile bool
}

// deadlineCandidates validates the whole page before any lifecycle side effect.
func deadlineCandidates(q meta.MQTTRead, r meta.MQTTReadResult) ([]deadlineCandidate, error) {
	if meta.ValidateMQTTRead(q) != nil || r.Session != nil || len(r.Subscriptions)+len(r.DeliveryCursors)+len(r.Inflight)+len(r.Bindings) != 0 {
		return nil, ErrDeadlineScanEvidence
	}
	count := len(r.Sessions) + len(r.Wills)
	if count > q.Limit || count == 0 && !r.Done || q.Kind == meta.MQTTReadSessionDeadlines && len(r.Wills) != 0 || q.Kind == meta.MQTTReadWillRecovery && len(r.Sessions) != 0 {
		return nil, ErrDeadlineScanEvidence
	}
	check := q
	check.After = r.After
	if meta.ValidateMQTTRead(check) != nil {
		return nil, ErrDeadlineScanEvidence
	}
	out := make([]deadlineCandidate, 0, count)
	for _, s := range r.Sessions {
		if meta.ValidateMQTTSession(s) != nil || s.State == meta.MQTTSessionEnded {
			return nil, ErrDeadlineScanEvidence
		}
		at := s.LeaseUntilMS
		if s.State == meta.MQTTSessionOffline {
			at = s.OfflineExpiresAtMS
		}
		o := contract.Owner{Key: contract.Key{Namespace: s.Namespace, ClientID: s.ClientID}, SessionGeneration: s.Generation, OwnerGeneration: s.OwnerGeneration, NodeID: s.OwnerNodeID, BootID: s.OwnerBootID, ConnectionID: s.ConnectionID}
		out = append(out, deadlineCandidate{at: at, owner: o, reconcile: true, cursor: meta.MQTTReadCursor{Deadline: meta.MQTTSessionDeadlineCursor{DeadlineMS: at, Namespace: s.Namespace, ClientID: s.ClientID}}})
	}
	for _, w := range r.Wills {
		if meta.ValidateMQTTWill(w) != nil || (w.Stage != meta.MQTTWillWaiting && w.Stage != meta.MQTTWillReady && w.Stage != meta.MQTTWillExecuting) {
			return nil, ErrDeadlineScanEvidence
		}
		at := w.DueAtMS
		if w.Stage == meta.MQTTWillExecuting {
			at = w.LeaseUntilMS
		}
		o := contract.Owner{Key: contract.Key{Namespace: w.Key.Namespace, ClientID: w.Key.ClientID}, SessionGeneration: w.Key.SessionGeneration, OwnerGeneration: w.OwnerGeneration, NodeID: w.OwnerNodeID, BootID: w.OwnerBootID, ConnectionID: w.ConnectionID}
		out = append(out, deadlineCandidate{at: at, owner: o, reconcile: w.Stage == meta.MQTTWillWaiting, cursor: meta.MQTTReadCursor{Will: meta.MQTTWillRecoveryCursor{RecoveryAtMS: at, Key: w.Key}}})
	}
	previous := q.After
	for _, c := range out {
		if c.owner.Validate() != nil || compareDeadlineCursor(q.Kind, previous, c.cursor) >= 0 {
			return nil, ErrDeadlineScanEvidence
		}
		previous = c.cursor
	}
	// A final storage page may retain its incoming cursor. Non-final pages must
	// point exactly at the last returned row, never beyond unvisited identities.
	if r.After != previous && (!r.Done || r.After != q.After) {
		return nil, ErrDeadlineScanEvidence
	}
	return out, nil
}

// compareDeadlineCursor matches the persisted index: numeric deadline, encoded
// length-before-bytes namespace/client strings, then complete Will generations.
func compareDeadlineCursor(kind meta.MQTTReadKind, a, b meta.MQTTReadCursor) int {
	at, bt := a.Deadline.DeadlineMS, b.Deadline.DeadlineMS
	an, bn, ac, bc := a.Deadline.Namespace, b.Deadline.Namespace, a.Deadline.ClientID, b.Deadline.ClientID
	if kind == meta.MQTTReadWillRecovery {
		at, bt = a.Will.RecoveryAtMS, b.Will.RecoveryAtMS
		an, bn = a.Will.Key.Namespace, b.Will.Key.Namespace
		ac, bc = a.Will.Key.ClientID, b.Will.Key.ClientID
	}
	if c := cmp.Compare(at, bt); c != 0 {
		return c
	}
	for _, p := range [][2]string{{an, bn}, {ac, bc}} {
		if c := cmp.Compare(len(p[0]), len(p[1])); c != 0 {
			return c
		}
		if c := strings.Compare(p[0], p[1]); c != 0 {
			return c
		}
	}
	if kind == meta.MQTTReadWillRecovery {
		if c := cmp.Compare(a.Will.Key.SessionGeneration, b.Will.Key.SessionGeneration); c != 0 {
			return c
		}
		return cmp.Compare(a.Will.Key.WillGeneration, b.Will.Key.WillGeneration)
	}
	return 0
}
