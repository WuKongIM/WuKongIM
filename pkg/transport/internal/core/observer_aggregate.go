package core

import "sync"

const observerAggregateKeysPerShard = 512
const observerDurationSampleEvery = 32

type observerAggregateKey struct {
	name          string
	node          NodeID
	service       uint16
	priority      Priority
	kind          FrameKind
	result        string
	frames, limit int
}

type observerAggregate struct {
	event    Event
	sequence uint64
	samples  DurationSamples
	duration bool
}

type observerAggregateShard struct {
	mu     sync.Mutex
	values map[observerAggregateKey]*observerAggregate
}

// aggregate retains exact bounded-label counters, and samples durations at a
// fixed one-in-32 rate independent of load or flush timing. Unsampled durations
// are intentional; sample overflow and unregistered event loss are reported.
func (d *ObserverDrain) aggregate(e Event) bool {
	var category uint64
	duration := false
	switch e.Name {
	case "sent_bytes":
		category = 1
	case "received_bytes":
		category = 2
	case "scheduler_admission":
		category = 3
	case "service_admission":
		category = 4
	case "write_batch":
		category = 5
	case "scheduler_wait":
		category = 6
		duration = true
	case "service_wait":
		category = 7
		duration = true
	case "service_task":
		category = 8
		duration = true
	case "client_rpc":
		category = 9
		duration = true
	default:
		return false
	}
	key := observerAggregateKey{name: e.Name, node: e.NodeID, service: e.ServiceID, priority: e.Priority, kind: e.Kind, result: e.Result}
	if e.Name == "write_batch" {
		key.frames = e.Items
		key.limit = e.Capacity
	}
	shard := &d.aggregates[(category+uint64(e.NodeID)*7+uint64(e.ServiceID)*3+uint64(e.Priority)+uint64(e.Kind))%uint64(len(d.aggregates))]
	shard.mu.Lock()
	if shard.values == nil {
		shard.values = make(map[observerAggregateKey]*observerAggregate)
	}
	a := shard.values[key]
	if a == nil {
		if len(shard.values) >= observerAggregateKeysPerShard {
			shard.mu.Unlock()
			d.dropped.Add(1)
			return true
		}
		a = &observerAggregate{duration: duration}
		shard.values[key] = a
	}
	count := a.event.Count + 1
	bytes := e.Bytes
	if e.Name == "sent_bytes" || e.Name == "received_bytes" || e.Name == "write_batch" {
		bytes += a.event.Bytes
	}
	a.event = e
	a.event.Count = count
	a.event.Bytes = bytes
	a.sequence++
	if duration && (a.sequence-1)%observerDurationSampleEvery == 0 {
		if a.samples.Len < len(a.samples.Values) {
			a.samples.Values[a.samples.Len] = e.Duration
			a.samples.Len++
		} else {
			d.dropped.Add(1)
		}
	}
	shard.mu.Unlock()
	return true
}

func (d *ObserverDrain) drainAggregates() {
	var batch []Event
	for i := range d.aggregates {
		shard := &d.aggregates[i]
		shard.mu.Lock()
		for _, a := range shard.values {
			if a.event.Count == 0 {
				continue
			}
			event := a.event
			if a.duration {
				samples := a.samples
				event.Samples = &samples
				a.samples = DurationSamples{}
			}
			batch = append(batch, event)
			a.event = Event{}
		}
		shard.mu.Unlock()
	}
	for _, event := range batch {
		d.target.ObserveTransport(event)
	}
	if dropped := d.dropped.Swap(0); dropped > 0 {
		d.target.ObserveTransport(Event{Name: "observer_dropped", Count: dropped})
	}
}
