package chatlifecycle

import "time"

// sessionExpiryDeadline is immutable after publication. An empty known hint
// differs from the nil/unknown hint used before a generation's first check.
// The bound includes leased sessions: a pin can require extra scans, never a
// delayed expiry. Canary anchors do not expire and never enter this bound.
type sessionExpiryDeadline struct {
	deadline time.Time
	present  bool
}

// sessionExpiryNotDue avoids the online-table lock only after expireAsync has
// validated its cleanup lifecycle and reservation state. The atomic read is
// its linearization point against a concurrently publishing Login.
func (p *SessionPool) sessionExpiryNotDue(now time.Time) bool {
	hint := p.expiryDeadline.Load()
	return hint != nil && (!hint.present || hint.deadline.After(now))
}

// noteSessionExpiryLocked publishes a new earlier deadline before Login
// releases mu and makes its complete online state observable. Removals need
// not update the bound: an obsolete earlier value can only cause extra work.
func (p *SessionPool) noteSessionExpiryLocked(session *onlineSession) {
	if session.canaryAnchor {
		return
	}
	hint := p.expiryDeadline.Load()
	if hint == nil || !hint.present || session.snapshot.Deadline.Before(hint.deadline) {
		p.publishSessionExpiryLocked(session.snapshot.Deadline, true)
	}
}

// publishSessionExpiryLocked replaces only changed immutable bounds. The due
// scan calls it after reserving the entire detach set, preserving the original
// all-or-nothing capacity failure. Future checks allocate and scan nothing.
func (p *SessionPool) publishSessionExpiryLocked(deadline time.Time, present bool) {
	previous := p.expiryDeadline.Load()
	if previous != nil && previous.present == present && (!present || previous.deadline.Equal(deadline)) {
		return
	}
	p.expiryDeadline.Store(&sessionExpiryDeadline{deadline: deadline, present: present})
}
