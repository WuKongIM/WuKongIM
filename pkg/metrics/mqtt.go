package metrics

import "github.com/prometheus/client_golang/prometheus"

// MQTTMetrics keeps fixed aggregate consumer and owner work series. Events count turn
// observations, including repeated confirmations, not unique Sessions/messages.
type MQTTMetrics struct {
	events                                 map[string]prometheus.Counter
	admitted, capacity                     prometheus.Gauge
	ownerTurns, ownerVisits, ownerFailures prometheus.Counter
	ownerWork                              map[string]prometheus.Gauge
}

func newMQTTMetrics(reg prometheus.Registerer, labels prometheus.Labels) *MQTTMetrics {
	v := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_mqtt_consumer_events_total", Help: "Bounded consumer maintenance turn observations; end confirmations may repeat and are not unique Session counts.", ConstLabels: labels}, []string{"event"})
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "wukongim_mqtt_consumer_work", Help: "Admitted consumer keys across queued and executing work, and the configured cohort bound.", ConstLabels: labels}, []string{"state"})
	m := &MQTTMetrics{events: make(map[string]prometheus.Counter), admitted: g.WithLabelValues("admitted"), capacity: g.WithLabelValues("capacity")}
	for _, event := range []string{"pages", "visited", "scheduled", "completed", "failures", "accounted", "projected", "removed", "quota_end_confirmed", "revocation_end_confirmed", "qualification_removed"} {
		m.events[event] = v.WithLabelValues(event)
	}
	s := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_mqtt_owner_sweep_total", Help: "Local owner sweep turns, visited owners and failed turns; visits are not retirement proofs.", ConstLabels: labels}, []string{"event"})
	m.ownerTurns, m.ownerVisits, m.ownerFailures = s.WithLabelValues("turns"), s.WithLabelValues("visited"), s.WithLabelValues("failures")
	w := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "wukongim_mqtt_owner_work", Help: "Last sampled aggregate local owner state; unresolved effects remain retained across sweep shutdown.", ConstLabels: labels}, []string{"state"})
	m.ownerWork = make(map[string]prometheus.Gauge)
	for _, state := range []string{"held", "pending", "active", "closing", "operations", "deadlines", "uncertain"} {
		m.ownerWork[state] = w.WithLabelValues(state)
	}
	reg.MustRegister(v, g, s, w)
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
