package goroutine

import (
	"context"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Registry manages goroutine lifecycle tracking across all subsystems.
type Registry struct {
	mu     sync.RWMutex
	groups map[groupKey]*group

	active      prometheus.GaugeVec
	totalStart  prometheus.CounterVec
	panicsTotal prometheus.CounterVec

	panicHandler func(component, name string, recovered any)
}

type groupKey struct {
	component string
	name      string
}

type group struct {
	active    atomic.Int64
	started   atomic.Int64
	stopped   atomic.Int64
	panics    atomic.Int64
	peak      atomic.Int64
	createdAt time.Time
}

// ComponentSnapshot describes goroutine state for one component.
type ComponentSnapshot struct {
	Component  string          `json:"component"`
	Active     int64           `json:"active"`
	TotalStart int64           `json:"total_started"`
	TotalStop  int64           `json:"total_stopped"`
	PanicCount int64           `json:"panics"`
	Groups     []GroupSnapshot `json:"groups"`
}

// GroupSnapshot describes goroutine state for one named group within a component.
type GroupSnapshot struct {
	Name       string `json:"name"`
	Active     int64  `json:"active"`
	Peak       int64  `json:"peak"`
	TotalStart int64  `json:"total_started"`
	PanicCount int64  `json:"panics"`
}

// Snapshot holds the full registry state at a point in time.
type Snapshot struct {
	TotalActive  int64               `json:"total_active"`
	TotalStarted int64               `json:"total_started"`
	TotalPanics  int64               `json:"total_panics"`
	Components   []ComponentSnapshot `json:"components"`
}

// Option configures a Registry.
type Option func(*Registry)

// WithPanicHandler sets a callback invoked after recovering a goroutine panic.
func WithPanicHandler(fn func(component, name string, recovered any)) Option {
	return func(r *Registry) { r.panicHandler = fn }
}

// WithPrometheusRegisterer registers goroutine metrics on the given registerer.
func WithPrometheusRegisterer(reg prometheus.Registerer) Option {
	return func(r *Registry) {
		reg.MustRegister(&r.active, &r.totalStart, &r.panicsTotal)
	}
}

// New creates a goroutine registry.
func New(opts ...Option) *Registry {
	r := &Registry{
		groups: make(map[groupKey]*group),
		active: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "wukongim_goroutines_active",
			Help: "Current active goroutines by component and name.",
		}, []string{"component", "name"}),
		totalStart: *prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_goroutines_started_total",
			Help: "Total goroutines started by component and name.",
		}, []string{"component", "name"}),
		panicsTotal: *prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_goroutines_panics_total",
			Help: "Total goroutine panics recovered by component and name.",
		}, []string{"component", "name"}),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Go starts a managed goroutine with automatic registration, deregistration, and panic recovery.
func (r *Registry) Go(component, name string, fn func()) {
	g := r.getOrCreateGroup(component, name)
	r.totalStart.WithLabelValues(component, name).Inc()
	g.started.Add(1)
	cur := g.active.Add(1)
	r.active.WithLabelValues(component, name).Set(float64(cur))
	updatePeak(&g.peak, cur)

	go func() {
		labels := pprof.Labels("component", component, "name", name)
		pprof.Do(context.Background(), labels, func(_ context.Context) {
			defer r.done(component, name, g)
			defer r.recoverPanic(component, name, g)
			fn()
		})
	}()
}

// GoN starts n identical managed goroutines.
func (r *Registry) GoN(component, name string, n int, fn func(workerID int)) {
	for i := 0; i < n; i++ {
		id := i
		r.Go(component, name, func() { fn(id) })
	}
}

// Snapshot returns the current registry state.
func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	componentMap := make(map[string]*ComponentSnapshot)
	var totalActive, totalStarted, totalPanics int64

	for key, g := range r.groups {
		active := g.active.Load()
		started := g.started.Load()
		panics := g.panics.Load()
		peak := g.peak.Load()

		totalActive += active
		totalStarted += started
		totalPanics += panics

		cs, ok := componentMap[key.component]
		if !ok {
			cs = &ComponentSnapshot{Component: key.component}
			componentMap[key.component] = cs
		}
		cs.Active += active
		cs.TotalStart += started
		cs.TotalStop += g.stopped.Load()
		cs.PanicCount += panics
		cs.Groups = append(cs.Groups, GroupSnapshot{
			Name:       key.name,
			Active:     active,
			Peak:       peak,
			TotalStart: started,
			PanicCount: panics,
		})
	}

	snap := Snapshot{
		TotalActive:  totalActive,
		TotalStarted: totalStarted,
		TotalPanics:  totalPanics,
	}
	for _, cs := range componentMap {
		snap.Components = append(snap.Components, *cs)
	}
	return snap
}

func (r *Registry) getOrCreateGroup(component, name string) *group {
	key := groupKey{component: component, name: name}
	r.mu.RLock()
	g, ok := r.groups[key]
	r.mu.RUnlock()
	if ok {
		return g
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok = r.groups[key]; ok {
		return g
	}
	g = &group{createdAt: time.Now()}
	r.groups[key] = g
	return g
}

func (r *Registry) done(component, name string, g *group) {
	g.stopped.Add(1)
	cur := g.active.Add(-1)
	r.active.WithLabelValues(component, name).Set(float64(cur))
}

func (r *Registry) recoverPanic(component, name string, g *group) {
	rec := recover()
	if rec == nil {
		return
	}
	g.panics.Add(1)
	r.panicsTotal.WithLabelValues(component, name).Inc()
	if r.panicHandler != nil {
		r.panicHandler(component, name, rec)
	}
}

func updatePeak(peak *atomic.Int64, cur int64) {
	for {
		old := peak.Load()
		if cur <= old {
			return
		}
		if peak.CompareAndSwap(old, cur) {
			return
		}
	}
}

// SafeGo starts a managed goroutine if r is non-nil, otherwise falls back to a plain go statement.
// This allows incremental adoption without nil checks at every call site.
func SafeGo(r *Registry, component, name string, fn func()) {
	if r == nil {
		go fn()
		return
	}
	r.Go(component, name, fn)
}

// SafeGoN starts n managed goroutines if r is non-nil, otherwise falls back to plain go statements.
func SafeGoN(r *Registry, component, name string, n int, fn func(workerID int)) {
	if r == nil {
		for i := 0; i < n; i++ {
			id := i
			go func() { fn(id) }()
		}
		return
	}
	r.GoN(component, name, n, fn)
}
