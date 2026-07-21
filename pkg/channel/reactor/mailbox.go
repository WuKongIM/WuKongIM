package reactor

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// Priority selects a mailbox queue.
type Priority uint8

const (
	PriorityHigh Priority = iota + 1
	PriorityNormal
	PriorityLow

	// maxConsecutiveHigh preserves completion/control priority while bounding
	// foreground append starvation when high-priority replication stays hot.
	maxConsecutiveHigh = 32
)

// MailboxConfig bounds each priority queue.
type MailboxConfig struct {
	HighSize   int
	NormalSize int
	LowSize    int
}

// Mailbox is a bounded priority mailbox.
type Mailbox struct {
	high            chan Event
	normal          chan Event
	low             chan Event
	consecutiveHigh int
}

type mailboxSubmitResult string

const (
	mailboxSubmitOK        mailboxSubmitResult = "ok"
	mailboxSubmitFull      mailboxSubmitResult = "full"
	mailboxSubmitClosed    mailboxSubmitResult = "closed"
	mailboxSubmitCoalesced mailboxSubmitResult = "coalesced"
)

// NewMailbox creates a bounded mailbox.
func NewMailbox(cfg MailboxConfig) *Mailbox {
	if cfg.HighSize <= 0 {
		cfg.HighSize = 1
	}
	if cfg.NormalSize <= 0 {
		cfg.NormalSize = 1
	}
	if cfg.LowSize <= 0 {
		cfg.LowSize = 1
	}
	return &Mailbox{high: make(chan Event, cfg.HighSize), normal: make(chan Event, cfg.NormalSize), low: make(chan Event, cfg.LowSize)}
}

// Submit attempts to enqueue an event without unbounded blocking.
func (m *Mailbox) Submit(priority Priority, event Event) error {
	_, err := m.submitWithResult(priority, event)
	return err
}

func (m *Mailbox) submitWithResult(priority Priority, event Event) (mailboxSubmitResult, error) {
	if m == nil {
		return mailboxSubmitClosed, ch.ErrClosed
	}
	queue := m.queue(priority)
	select {
	case queue <- event:
		return mailboxSubmitOK, nil
	default:
		if priority == PriorityLow {
			// Low-priority tick work is droppable/coalesced; complete futures on drop so direct callers cannot hang.
			if event.Future != nil {
				event.Future.Complete(Result{})
			}
			return mailboxSubmitCoalesced, nil
		}
		return mailboxSubmitFull, ch.ErrBackpressured
	}
}

func (m *Mailbox) submitBlockingWithResult(priority Priority, event Event, stop <-chan struct{}) (mailboxSubmitResult, error) {
	if m == nil {
		return mailboxSubmitClosed, ch.ErrClosed
	}
	queue := m.queue(priority)
	select {
	case queue <- event:
		return mailboxSubmitOK, nil
	case <-stop:
		return mailboxSubmitClosed, ch.ErrClosed
	}
}

// Drain removes up to max events with bounded high-priority preference.
func (m *Mailbox) Drain(max int) []Event {
	if max <= 0 {
		return nil
	}
	return m.DrainInto(make([]Event, 0, max), max)
}

// DrainInto removes up to max events into dst. High priority is preferred, but
// one waiting normal event is admitted after a bounded high-priority burst.
func (m *Mailbox) DrainInto(dst []Event, max int) []Event {
	if m == nil || max <= 0 {
		return dst[:0]
	}
	events := dst[:0]
	for len(events) < max {
		if m.consecutiveHigh >= maxConsecutiveHigh {
			if event, ok := tryRecv(m.normal); ok {
				m.recordDequeue(PriorityNormal)
				events = append(events, event)
				continue
			}
		}
		if event, ok := tryRecv(m.high); ok {
			m.recordDequeue(PriorityHigh)
			events = append(events, event)
			continue
		}
		if event, ok := tryRecv(m.normal); ok {
			m.recordDequeue(PriorityNormal)
			events = append(events, event)
			continue
		}
		if event, ok := tryRecv(m.low); ok {
			events = append(events, event)
			continue
		}
		break
	}
	return events
}

// WaitOne blocks until one event, stop, or timer is ready.
func (m *Mailbox) WaitOne(stop <-chan struct{}, timer <-chan time.Time) (Event, bool) {
	if m == nil {
		return Event{}, false
	}
	if m.consecutiveHigh >= maxConsecutiveHigh {
		if event, ok := tryRecv(m.normal); ok {
			m.recordDequeue(PriorityNormal)
			return event, true
		}
	}
	select {
	case event := <-m.high:
		m.recordDequeue(PriorityHigh)
		return event, true
	case event := <-m.normal:
		m.recordDequeue(PriorityNormal)
		return event, true
	case event := <-m.low:
		return event, true
	case <-stop:
		return Event{}, false
	case <-timer:
		return Event{}, false
	}
}

func (m *Mailbox) recordDequeue(priority Priority) {
	if priority == PriorityNormal {
		m.consecutiveHigh = 0
		return
	}
	if priority == PriorityHigh && m.consecutiveHigh < maxConsecutiveHigh {
		m.consecutiveHigh++
	}
}

// Depth returns the current queued event count for one priority.
func (m *Mailbox) Depth(priority Priority) int {
	if m == nil {
		return 0
	}
	switch priority {
	case PriorityHigh:
		return len(m.high)
	case PriorityLow:
		return len(m.low)
	default:
		return len(m.normal)
	}
}

// Capacity returns the configured capacity for one priority queue.
func (m *Mailbox) Capacity(priority Priority) int {
	if m == nil {
		return 0
	}
	switch priority {
	case PriorityHigh:
		return cap(m.high)
	case PriorityLow:
		return cap(m.low)
	default:
		return cap(m.normal)
	}
}

func (m *Mailbox) queue(priority Priority) chan Event {
	switch priority {
	case PriorityHigh:
		return m.high
	case PriorityLow:
		return m.low
	default:
		return m.normal
	}
}

func tryRecv(queue <-chan Event) (Event, bool) {
	select {
	case event := <-queue:
		return event, true
	default:
		return Event{}, false
	}
}
