package diagnostics

import (
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultMaxTrackingRules bounds operator-created diagnostics tracking rules per node.
	DefaultMaxTrackingRules = 100
	// DefaultMaxTrackingTTL bounds how long one diagnostics tracking rule can stay active.
	DefaultMaxTrackingTTL = 24 * time.Hour
)

var (
	// ErrInvalidTrackingRule reports malformed diagnostics tracking rule input.
	ErrInvalidTrackingRule = errors.New("diagnostics: invalid tracking rule")
	// ErrTrackingRuleLimit reports that the node-local tracking rule limit was reached.
	ErrTrackingRuleLimit = errors.New("diagnostics: tracking rule limit reached")
)

// TrackingTarget identifies the diagnostics field matched by a runtime tracking rule.
type TrackingTarget string

const (
	// TrackingTargetChannel matches diagnostics events by ChannelKey.
	TrackingTargetChannel TrackingTarget = "channel"
	// TrackingTargetSenderUID matches diagnostics events by sender FromUID.
	TrackingTargetSenderUID TrackingTarget = "sender_uid"
)

// TrackingRuleInput describes one runtime diagnostics tracking rule mutation.
type TrackingRuleInput struct {
	// ID identifies the rule across all nodes and makes add operations idempotent.
	ID string
	// Target identifies whether UID or ChannelKey is matched.
	Target TrackingTarget
	// UID matches Event.FromUID when Target is sender_uid.
	UID string
	// ChannelKey matches Event.ChannelKey when Target is channel.
	ChannelKey string
	// TTL controls how long the rule stays active from installation time.
	TTL time.Duration
	// SampleRate is the keep probability applied to matching events.
	SampleRate float64
}

// TrackingRule is the node-local runtime view of one diagnostics tracking rule.
type TrackingRule struct {
	// ID identifies the rule across all nodes.
	ID string `json:"rule_id"`
	// Target identifies whether UID or ChannelKey is matched.
	Target TrackingTarget `json:"target"`
	// UID matches Event.FromUID when Target is sender_uid.
	UID string `json:"uid,omitempty"`
	// ChannelKey matches Event.ChannelKey when Target is channel.
	ChannelKey string `json:"channel_key,omitempty"`
	// SampleRate is the keep probability applied to matching events.
	SampleRate float64 `json:"sample_rate"`
	// CreatedAt records when this node installed the rule.
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt records when this node stops applying the rule.
	ExpiresAt time.Time `json:"expires_at"`
}

// TrackingRulesOptions configures the bounded runtime tracking rule store.
type TrackingRulesOptions struct {
	// MaxRules bounds active operator-created rules.
	MaxRules int
	// MaxTTL bounds the TTL accepted for one rule.
	MaxTTL time.Duration
	// Now supplies timestamps for rule installation and expiry checks.
	Now func() time.Time
}

type trackingRuleState struct {
	rule          TrackingRule
	counter       atomic.Uint64
	detailCounter atomic.Uint64
}

// TrackingRules stores dynamic, TTL-bound diagnostics sampling rules.
type TrackingRules struct {
	mu       sync.Mutex
	rules    map[string]*trackingRuleState
	snapshot atomic.Value // []*trackingRuleState
	maxRules int
	maxTTL   time.Duration
	now      func() time.Time
}

// NewTrackingRules creates a bounded diagnostics tracking rule store.
func NewTrackingRules(opts TrackingRulesOptions) *TrackingRules {
	if opts.MaxRules <= 0 {
		opts.MaxRules = DefaultMaxTrackingRules
	}
	if opts.MaxTTL <= 0 {
		opts.MaxTTL = DefaultMaxTrackingTTL
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	rules := &TrackingRules{
		rules:    make(map[string]*trackingRuleState),
		maxRules: opts.MaxRules,
		maxTTL:   opts.MaxTTL,
		now:      opts.Now,
	}
	rules.snapshot.Store([]*trackingRuleState{})
	return rules
}

// Add validates and installs a runtime diagnostics tracking rule.
func (r *TrackingRules) Add(input TrackingRuleInput) (TrackingRule, error) {
	if r == nil {
		return TrackingRule{}, ErrInvalidTrackingRule
	}
	input.ID = strings.TrimSpace(input.ID)
	input.UID = strings.TrimSpace(input.UID)
	input.ChannelKey = strings.TrimSpace(input.ChannelKey)
	if err := r.validateInput(input); err != nil {
		return TrackingRule{}, err
	}

	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	if _, exists := r.rules[input.ID]; !exists && len(r.rules) >= r.maxRules {
		return TrackingRule{}, ErrTrackingRuleLimit
	}

	state := &trackingRuleState{rule: TrackingRule{
		ID:         input.ID,
		Target:     input.Target,
		UID:        input.UID,
		ChannelKey: input.ChannelKey,
		SampleRate: input.SampleRate,
		CreatedAt:  now,
		ExpiresAt:  now.Add(input.TTL),
	}}
	r.rules[input.ID] = state
	r.publishLocked()
	return state.rule, nil
}

// List returns active runtime diagnostics tracking rules in stable ID order.
func (r *TrackingRules) List() []TrackingRule {
	if r == nil {
		return nil
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	out := make([]TrackingRule, 0, len(r.rules))
	for _, state := range r.rules {
		out = append(out, state.rule)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Delete removes a runtime diagnostics tracking rule by id.
func (r *TrackingRules) Delete(id string) bool {
	if r == nil {
		return false
	}
	id = strings.TrimSpace(id)
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	if _, ok := r.rules[id]; !ok {
		return false
	}
	delete(r.rules, id)
	r.publishLocked()
	return true
}

// Keep reports whether a dynamic tracking rule keeps event.
func (r *TrackingRules) Keep(event Event) bool {
	return r.keep(event, false)
}

// KeepDetail reports whether a dynamic tracking rule keeps expensive deep trace detail.
func (r *TrackingRules) KeepDetail(event Event) bool {
	return r.keep(event, true)
}

func (r *TrackingRules) keep(event Event, detail bool) bool {
	if r == nil {
		return false
	}
	rules, _ := r.snapshot.Load().([]*trackingRuleState)
	if len(rules) == 0 {
		return false
	}
	now := r.now()
	for _, state := range rules {
		if state == nil || now.After(state.rule.ExpiresAt) || !trackingRuleMatches(state.rule, event) {
			continue
		}
		counter := &state.counter
		if detail {
			counter = &state.detailCounter
		}
		if keepByRate(state.rule.SampleRate, counter) {
			return true
		}
	}
	return false
}

func (r *TrackingRules) validateInput(input TrackingRuleInput) error {
	if input.ID == "" || input.TTL <= 0 || input.TTL > r.maxTTL || math.IsNaN(input.SampleRate) || input.SampleRate < 0 || input.SampleRate > 1 {
		return ErrInvalidTrackingRule
	}
	switch input.Target {
	case TrackingTargetSenderUID:
		if input.UID == "" {
			return ErrInvalidTrackingRule
		}
	case TrackingTargetChannel:
		if input.ChannelKey == "" {
			return ErrInvalidTrackingRule
		}
	default:
		return ErrInvalidTrackingRule
	}
	return nil
}

func (r *TrackingRules) pruneLocked(now time.Time) {
	changed := false
	for id, state := range r.rules {
		if state == nil || now.After(state.rule.ExpiresAt) {
			delete(r.rules, id)
			changed = true
		}
	}
	if changed {
		r.publishLocked()
	}
}

func (r *TrackingRules) publishLocked() {
	snapshot := make([]*trackingRuleState, 0, len(r.rules))
	for _, state := range r.rules {
		snapshot = append(snapshot, state)
	}
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].rule.ID < snapshot[j].rule.ID })
	r.snapshot.Store(snapshot)
}

func trackingRuleMatches(rule TrackingRule, event Event) bool {
	switch rule.Target {
	case TrackingTargetSenderUID:
		return rule.UID != "" && event.FromUID == rule.UID
	case TrackingTargetChannel:
		return rule.ChannelKey != "" && event.ChannelKey == rule.ChannelKey
	default:
		return false
	}
}
