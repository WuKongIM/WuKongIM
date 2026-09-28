package metrics

import "github.com/prometheus/client_golang/prometheus"

// MQTTMetrics keeps fixed aggregate consumer and owner work series. Events count turn
// observations, including repeated confirmations, not unique Sessions/messages.
type MQTTMetrics struct {
	subscriptionClosures                   map[[2]string]prometheus.Counter
	events                                 map[string]prometheus.Counter
	admitted, capacity                     prometheus.Gauge
	ownerTurns, ownerVisits, ownerFailures prometheus.Counter
	ownerWork                              map[string]prometheus.Gauge
}

func newMQTTMetrics(reg prometheus.Registerer, labels prometheus.Labels) *MQTTMetrics {
	v := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_mqtt_consumer_events_total", Help: "Bounded consumer maintenance turn observations; end confirmations may repeat and are not unique Session counts.", ConstLabels: labels}, []string{"event"})
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "wukongim_mqtt_consumer_work", Help: "Admitted consumer keys across queued and executing work, and the configured cohort bound.", ConstLabels: labels}, []string{"state"})
	m := &MQTTMetrics{events: make(map[string]prometheus.Counter), admitted: g.WithLabelValues("admitted"), capacity: g.WithLabelValues("capacity")}
	for _, event := range []string{"pages", "visited", "scheduled", "completed", "failures", "accounted", "projected", "removed", "retired", "quota_end_confirmed", "revocation_end_confirmed", "qualification_removed", "subscription_removal_confirmed", "subscription_establishment_confirmed", "reclamation_confirmed", "reclamation_index_rows"} {
		m.events[event] = v.WithLabelValues(event)
	}
	s := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_mqtt_owner_sweep_total", Help: "Local owner sweep turns, visited owners and failed turns; visits are not retirement proofs.", ConstLabels: labels}, []string{"event"})
	m.ownerTurns, m.ownerVisits, m.ownerFailures = s.WithLabelValues("turns"), s.WithLabelValues("visited"), s.WithLabelValues("failures")
	w := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "wukongim_mqtt_owner_work", Help: "Last sampled aggregate local owner state; unresolved effects remain retained across sweep shutdown.", ConstLabels: labels}, []string{"state"})
	m.ownerWork = make(map[string]prometheus.Gauge)
	for _, state := range []string{"held", "pending", "active", "closing", "operations", "deadlines", "uncertain"} {
		m.ownerWork[state] = w.WithLabelValues(state)
	}
	closure := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_mqtt_subscription_closures_total", Help: "SUB/UNSUB entry close requests by fixed failure reason; not unique connections or proof of isolation.", ConstLabels: labels}, []string{"operation", "reason"})
	m.subscriptionClosures = make(map[[2]string]prometheus.Counter, 34)
	for _, operation := range []string{"subscribe", "unsubscribe"} {
		for _, reason := range []string{"disabled", "malformed", "owner_limit", "fenced", "deadline", "canceled", "clock", "conflict", "evidence", "pending", "unconfirmed", "denied", "quota", "callback", "reply_evidence", "reply_write", "unknown"} {
			m.subscriptionClosures[[2]string{operation, reason}] = closure.WithLabelValues(operation, reason)
		}
	}
	reg.MustRegister(v, g, s, w, closure)
	return m
}

// ObserveConsumer drops unknown event names; caller-provided identities never
// become Prometheus labels. Zero series exist before the first work observation.
func (m *MQTTMetrics) ObserveConsumer(event string, count uint64) {
	if m == nil {
		return
	}
	if c := m.events[event]; c != nil {
		c.Add(float64(count))
	}
}
func (m *MQTTMetrics) SetConsumerWork(admitted, capacity int) {
	if m == nil {
		return
	}
	m.admitted.Set(float64(max(0, admitted)))
	m.capacity.Set(float64(max(0, capacity)))
}

// ObserveOwnerSweep records bounded turn outcomes without error or owner labels.
func (m *MQTTMetrics) ObserveOwnerSweep(visited int, failed bool) {
	if m == nil {
		return
	}
	m.ownerTurns.Inc()
	m.ownerVisits.Add(float64(max(0, visited)))
	if failed {
		m.ownerFailures.Inc()
	}
}

// SetOwnerWork accepts only fixed aggregate names and never invents labels.
func (m *MQTTMetrics) SetOwnerWork(state string, value int) {
	if m == nil {
		return
	}
	if g := m.ownerWork[state]; g != nil {
		g.Set(float64(max(0, value)))
	}
}

// ObserveSubscriptionClose accepts only pre-materialized operation/reason pairs.
// It never creates series from identities, topics or dependency error text.
func (m *MQTTMetrics) ObserveSubscriptionClose(operation, reason string) {
	if m == nil {
		return
	}
	if c := m.subscriptionClosures[[2]string{operation, reason}]; c != nil {
		c.Inc()
	}
}
