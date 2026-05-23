package reactor

import ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"

// Priority selects a mailbox queue.
type Priority uint8

const (
	PriorityHigh Priority = iota + 1
	PriorityNormal
	PriorityLow
)

// MailboxConfig bounds each priority queue.
type MailboxConfig struct {
	HighSize   int
	NormalSize int
	LowSize    int
}

// Mailbox is a bounded priority mailbox.
type Mailbox struct {
	high   chan Event
	normal chan Event
	low    chan Event
}

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
	if m == nil {
		return ch.ErrClosed
	}
	queue := m.normal
	switch priority {
	case PriorityHigh:
		queue = m.high
	case PriorityLow:
		queue = m.low
	}
	select {
	case queue <- event:
		return nil
	default:
		if priority == PriorityLow {
			// Low-priority tick work is droppable/coalesced; complete futures on drop so direct callers cannot hang.
			if event.Future != nil {
				event.Future.Complete(Result{})
			}
			return nil
		}
		return ch.ErrBackpressured
	}
}

// Drain removes up to max events, honoring high before normal before low.
func (m *Mailbox) Drain(max int) []Event {
	if m == nil || max <= 0 {
		return nil
	}
	events := make([]Event, 0, max)
	for len(events) < max {
		if event, ok := tryRecv(m.high); ok {
			events = append(events, event)
			continue
		}
		if event, ok := tryRecv(m.normal); ok {
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

func tryRecv(queue <-chan Event) (Event, bool) {
	select {
	case event := <-queue:
		return event, true
	default:
		return Event{}, false
	}
}
