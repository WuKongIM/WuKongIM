// Package chatlifecyclerepair owns the bounded, phase-aware decision state for
// one reusable cloud repair Lease. It does not provision, deploy, or release
// infrastructure; entry adapters execute the returned action.
package chatlifecyclerepair

import (
	"errors"
	"regexp"
	"time"
)

const (
	StateSchemaV1       = "wukongim.chat_lifecycle.repair_state/v1"
	ObservationSchemaV1 = "wukongim.chat_lifecycle.repair_observation/v1"
)

var (
	ErrInvalidConfig      = errors.New("chat lifecycle repair: invalid config")
	ErrInvalidCandidate   = errors.New("chat lifecycle repair: invalid candidate")
	ErrInvalidObservation = errors.New("chat lifecycle repair: invalid observation")
	ErrGenerationTerminal = errors.New("chat lifecycle repair: generation is terminal")
	identityPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	shaPattern            = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern         = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Phase tells the monitor whether zero traffic is expected. Only active
// traffic is subject to online and progress fail-fast rules.
type Phase string

const (
	PhaseDeploying Phase = "deploying"
	PhaseWarmup    Phase = "warmup"
	PhaseActive    Phase = "active"
	PhaseDrain     Phase = "drain"
)

// Action is the only control decision exposed to workflow adapters.
type Action string

const (
	ActionContinue        Action = "continue"
	ActionStopAndDiagnose Action = "stop_and_diagnose"
	ActionQualified       Action = "qualified"
)

// Reason is a bounded, identity-free decision classification.
type Reason string

const (
	ReasonNone                           Reason = "none"
	ReasonWarmupTimeout                  Reason = "warmup_timeout"
	ReasonMessageProgressStalled         Reason = "message_progress_stalled"
	ReasonAcknowledgementProgressStalled Reason = "acknowledgement_progress_stalled"
	ReasonAcknowledgementBacklogExceeded Reason = "acknowledgement_backlog_exceeded"
	ReasonSendRateBelowFloor             Reason = "send_rate_below_floor"
	ReasonOnlineBelowFloor               Reason = "online_below_floor"
	ReasonActivePhaseLost                Reason = "active_phase_lost"
	ReasonTerminalError                  Reason = "terminal_error"
	ReasonServiceInactive                Reason = "service_inactive"
	ReasonObservationUnavailable         Reason = "observation_unavailable"
	ReasonMonitorTimeout                 Reason = "monitor_timeout"
	ReasonOperatorStop                   Reason = "operator_stop"
)

// Config fixes one short-run qualification contract.
type Config struct {
	TargetOnline             uint64        `json:"target_online"`
	MinimumOnlinePercent     uint64        `json:"minimum_online_percent"`
	WarmupTimeout            time.Duration `json:"warmup_timeout"`
	StallAfter               time.Duration `json:"stall_after"`
	QualifyAfter             time.Duration `json:"qualify_after"`
	MinimumSendRatePerSecond uint64        `json:"minimum_send_rate_per_second"`
	MaximumAckBacklog        uint64        `json:"maximum_acknowledgement_backlog"`
}

// Candidate identifies one immutable protected-main bundle generation.
type Candidate struct {
	RequestID    string `json:"request_id"`
	LeaseID      string `json:"lease_id"`
	Generation   uint64 `json:"generation"`
	SourceSHA    string `json:"source_sha"`
	BundleDigest string `json:"bundle_digest"`
}

// Observation is the bounded aggregate produced by exactly one sampling cut.
type Observation struct {
	Schema           string    `json:"schema"`
	RequestID        string    `json:"request_id"`
	LeaseID          string    `json:"lease_id"`
	Generation       uint64    `json:"generation"`
	ObservedAt       time.Time `json:"observed_at"`
	Phase            Phase     `json:"phase"`
	Online           uint64    `json:"online"`
	Sent             uint64    `json:"sent"`
	SendAcknowledged uint64    `json:"send_acknowledged"`
	TerminalErrors   uint64    `json:"terminal_errors"`
}

// State is the complete restart-safe monitor state for one generation.
type State struct {
	Schema             string     `json:"schema"`
	Config             Config     `json:"config"`
	Candidate          Candidate  `json:"candidate"`
	StartedAt          time.Time  `json:"started_at"`
	LastObservedAt     time.Time  `json:"last_observed_at,omitempty"`
	ActiveStartedAt    time.Time  `json:"active_started_at,omitempty"`
	EverActive         bool       `json:"ever_active"`
	ActiveLostSince    *time.Time `json:"active_lost_since,omitempty"`
	LastSendProgressAt time.Time  `json:"last_send_progress_at,omitempty"`
	LastAckProgressAt  time.Time  `json:"last_acknowledgement_progress_at,omitempty"`
	SendRateBelowSince *time.Time `json:"send_rate_below_since,omitempty"`
	AckBacklogSince    *time.Time `json:"acknowledgement_backlog_since,omitempty"`
	OnlineBelowSince   *time.Time `json:"online_below_since,omitempty"`
	LastOnline         uint64     `json:"last_online"`
	LastSent           uint64     `json:"last_sent"`
	LastAcknowledged   uint64     `json:"last_send_acknowledged"`
	LastTerminalErrors uint64     `json:"last_terminal_errors"`
	TerminalAction     Action     `json:"terminal_action,omitempty"`
	TerminalReason     Reason     `json:"terminal_reason,omitempty"`
}

// Decision directs the entry adapter without exposing raw snapshot data.
type Decision struct {
	Action     Action    `json:"action"`
	Reason     Reason    `json:"reason"`
	ObservedAt time.Time `json:"observed_at"`
	Generation uint64    `json:"generation"`
}

// Begin creates the restart-safe state for one candidate generation.
func Begin(config Config, candidate Candidate, startedAt time.Time) (State, error) {
	if !validConfig(config) {
		return State{}, ErrInvalidConfig
	}
	if !validCandidate(candidate) || startedAt.IsZero() {
		return State{}, ErrInvalidCandidate
	}
	return State{Schema: StateSchemaV1, Config: config, Candidate: candidate, StartedAt: startedAt.UTC()}, nil
}

// Advance validates one monotonic observation and returns the next durable
// state plus the single workflow action justified by that evidence.
func Advance(config Config, state State, observation Observation) (State, Decision, error) {
	if !validConfig(config) || !validState(state) || config != state.Config {
		return state, Decision{}, ErrInvalidObservation
	}
	if state.TerminalAction != "" {
		return state, Decision{}, ErrGenerationTerminal
	}
	if !validObservation(state, observation) {
		return state, Decision{}, ErrInvalidObservation
	}
	next := state
	at := observation.ObservedAt.UTC()
	decision := Decision{Action: ActionContinue, Reason: ReasonNone, ObservedAt: at, Generation: state.Candidate.Generation}

	if observation.TerminalErrors > state.LastTerminalErrors {
		decision.Action, decision.Reason = ActionStopAndDiagnose, ReasonTerminalError
	}
	if observation.Phase == PhaseActive {
		continuedActive := !state.ActiveStartedAt.IsZero() && state.ActiveLostSince == nil && !state.LastObservedAt.IsZero()
		if next.ActiveStartedAt.IsZero() || next.ActiveLostSince != nil {
			next.ActiveStartedAt = at
			next.EverActive = true
			next.ActiveLostSince = nil
			next.LastSendProgressAt = at
			next.LastAckProgressAt = at
			next.SendRateBelowSince = nil
			next.AckBacklogSince = nil
		}
		if observation.Sent > state.LastSent {
			next.LastSendProgressAt = at
		}
		if observation.SendAcknowledged > state.LastAcknowledged {
			next.LastAckProgressAt = at
		}
		floor := minimumOnline(config)
		if observation.Online < floor {
			if next.OnlineBelowSince == nil {
				below := at
				next.OnlineBelowSince = &below
			}
		} else {
			next.OnlineBelowSince = nil
		}
		activeElapsed := at.Sub(next.ActiveStartedAt)
		if continuedActive && observation.Sent-state.LastSent < minimumSentProgress(config, at.Sub(state.LastObservedAt)) {
			if next.SendRateBelowSince == nil {
				below := at
				next.SendRateBelowSince = &below
			}
		} else if continuedActive {
			next.SendRateBelowSince = nil
		}
		if observation.Sent-observation.SendAcknowledged > config.MaximumAckBacklog {
			if next.AckBacklogSince == nil {
				above := at
				next.AckBacklogSince = &above
			}
		} else {
			next.AckBacklogSince = nil
		}
		if decision.Action == ActionContinue && next.OnlineBelowSince != nil && at.Sub(*next.OnlineBelowSince) >= config.StallAfter {
			decision.Action, decision.Reason = ActionStopAndDiagnose, ReasonOnlineBelowFloor
		}
		if decision.Action == ActionContinue && at.Sub(next.LastSendProgressAt) >= config.StallAfter {
			decision.Action, decision.Reason = ActionStopAndDiagnose, ReasonMessageProgressStalled
		}
		if decision.Action == ActionContinue && observation.Sent > 0 && at.Sub(next.LastAckProgressAt) >= config.StallAfter {
			decision.Action, decision.Reason = ActionStopAndDiagnose, ReasonAcknowledgementProgressStalled
		}
		if decision.Action == ActionContinue && next.SendRateBelowSince != nil && at.Sub(*next.SendRateBelowSince) >= config.StallAfter {
			decision.Action, decision.Reason = ActionStopAndDiagnose, ReasonSendRateBelowFloor
		}
		if decision.Action == ActionContinue && next.AckBacklogSince != nil && at.Sub(*next.AckBacklogSince) >= config.StallAfter {
			decision.Action, decision.Reason = ActionStopAndDiagnose, ReasonAcknowledgementBacklogExceeded
		}
		if decision.Action == ActionContinue && activeElapsed >= config.QualifyAfter &&
			observation.Online >= floor && at.Sub(next.LastSendProgressAt) < config.StallAfter &&
			at.Sub(next.LastAckProgressAt) < config.StallAfter && next.SendRateBelowSince == nil &&
			next.AckBacklogSince == nil {
			decision.Action = ActionQualified
		}
	} else {
		if next.EverActive {
			if next.ActiveLostSince == nil {
				lost := at
				next.ActiveLostSince = &lost
			}
			if decision.Action == ActionContinue && at.Sub(*next.ActiveLostSince) >= config.StallAfter {
				decision.Action, decision.Reason = ActionStopAndDiagnose, ReasonActivePhaseLost
			}
		} else if decision.Action == ActionContinue && observation.Phase != PhaseDrain && at.Sub(state.StartedAt) >= config.WarmupTimeout {
			decision.Action, decision.Reason = ActionStopAndDiagnose, ReasonWarmupTimeout
		}
	}

	next.LastObservedAt = at
	next.LastOnline = observation.Online
	next.LastSent = observation.Sent
	next.LastAcknowledged = observation.SendAcknowledged
	next.LastTerminalErrors = observation.TerminalErrors
	if decision.Action == ActionStopAndDiagnose || decision.Action == ActionQualified {
		next.TerminalAction = decision.Action
		next.TerminalReason = decision.Reason
	}
	return next, decision, nil
}

// Abort seals a monitor-owned infrastructure failure without fabricating a
// worker observation. Only the closed external reason set is accepted.
func Abort(state State, observedAt time.Time, reason Reason) (State, Decision, error) {
	if !validState(state) || observedAt.IsZero() || observedAt.Before(state.StartedAt) ||
		(!state.LastObservedAt.IsZero() && !observedAt.After(state.LastObservedAt)) || !externalAbortReason(reason) {
		return state, Decision{}, ErrInvalidObservation
	}
	if state.TerminalAction != "" {
		return state, Decision{}, ErrGenerationTerminal
	}
	at := observedAt.UTC()
	state.LastObservedAt = at
	state.TerminalAction = ActionStopAndDiagnose
	state.TerminalReason = reason
	return state, Decision{Action: ActionStopAndDiagnose, Reason: reason, ObservedAt: at, Generation: state.Candidate.Generation}, nil
}

func externalAbortReason(reason Reason) bool {
	switch reason {
	case ReasonServiceInactive, ReasonObservationUnavailable, ReasonMonitorTimeout, ReasonOperatorStop:
		return true
	default:
		return false
	}
}

func validConfig(config Config) bool {
	return config.TargetOnline > 0 && config.TargetOnline <= 1_000_000 &&
		config.MinimumOnlinePercent > 0 && config.MinimumOnlinePercent <= 100 &&
		config.MinimumSendRatePerSecond > 0 && config.MinimumSendRatePerSecond <= 1_000_000 &&
		config.MaximumAckBacklog > 0 && config.MaximumAckBacklog <= 100_000_000 &&
		config.StallAfter >= time.Second && config.WarmupTimeout >= config.StallAfter &&
		config.WarmupTimeout <= 10*time.Minute && config.QualifyAfter >= config.StallAfter &&
		config.QualifyAfter <= 10*time.Minute
}

func validCandidate(candidate Candidate) bool {
	return identityPattern.MatchString(candidate.RequestID) && identityPattern.MatchString(candidate.LeaseID) &&
		candidate.Generation > 0 && shaPattern.MatchString(candidate.SourceSHA) && digestPattern.MatchString(candidate.BundleDigest)
}

func validState(state State) bool {
	if state.Schema != StateSchemaV1 || !validConfig(state.Config) || !validCandidate(state.Candidate) || state.StartedAt.IsZero() {
		return false
	}
	if state.TerminalAction == "" {
		return state.TerminalReason == ""
	}
	return (state.TerminalAction == ActionStopAndDiagnose || state.TerminalAction == ActionQualified) && state.TerminalReason != ""
}

func validObservation(state State, observation Observation) bool {
	if observation.Schema != ObservationSchemaV1 || observation.RequestID != state.Candidate.RequestID ||
		observation.LeaseID != state.Candidate.LeaseID || observation.Generation != state.Candidate.Generation ||
		observation.ObservedAt.IsZero() || observation.ObservedAt.Before(state.StartedAt) ||
		(!state.LastObservedAt.IsZero() && !observation.ObservedAt.After(state.LastObservedAt)) ||
		observation.Sent < state.LastSent || observation.SendAcknowledged < state.LastAcknowledged ||
		observation.TerminalErrors < state.LastTerminalErrors || observation.SendAcknowledged > observation.Sent {
		return false
	}
	switch observation.Phase {
	case PhaseDeploying, PhaseWarmup, PhaseActive, PhaseDrain:
		return true
	default:
		return false
	}
}

func minimumOnline(config Config) uint64 {
	whole := (config.TargetOnline / 100) * config.MinimumOnlinePercent
	remainder := (config.TargetOnline % 100) * config.MinimumOnlinePercent
	return whole + (remainder+99)/100
}

func minimumSentProgress(config Config, elapsed time.Duration) uint64 {
	return uint64(elapsed/time.Second) * config.MinimumSendRatePerSecond
}
