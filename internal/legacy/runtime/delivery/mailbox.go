package delivery

type mailbox struct {
	limit  int
	events []any
}

func newMailbox(limit int) *mailbox {
	return &mailbox{limit: limit}
}

func (m *mailbox) push(event any) bool {
	if m == nil {
		return false
	}
	if m.limit > 0 && len(m.events) >= m.limit {
		return false
	}
	m.events = append(m.events, event)
	return true
}

func (m *mailbox) drain() []any {
	if m == nil || len(m.events) == 0 {
		return nil
	}
	out := append([]any(nil), m.events...)
	m.events = m.events[:0]
	return out
}

func (m *mailbox) depth() int {
	if m == nil {
		return 0
	}
	return len(m.events)
}
