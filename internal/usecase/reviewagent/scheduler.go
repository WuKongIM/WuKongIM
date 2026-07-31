package reviewagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

const (
	maxSchedulerQueue      = 10000
	maxSchedulerStateBytes = 512 << 10
)

var schedulerDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// QueueEntry is one immutable generation waiting for a repository lease.
type QueueEntry struct {
	Generation        contract.GenerationIdentity `json:"generation"`
	FirstTimeExternal bool                        `json:"first_time_external"`
	EnqueuedAt        time.Time                   `json:"enqueued_at"`
}

// Lease binds one active generation to the exact Actions run responsible for
// its completion.
type Lease struct {
	Generation        contract.GenerationIdentity `json:"generation"`
	RunID             int64                       `json:"run_id"`
	FirstTimeExternal bool                        `json:"first_time_external"`
	AcquiredAt        time.Time                   `json:"acquired_at"`
}

// SchedulerState is the signed FIFO and active-lease document.
type SchedulerState struct {
	SchemaVersion       int          `json:"schema_version"`
	SourceSHA           string       `json:"source_sha"`
	Sequence            uint64       `json:"sequence"`
	PreviousStateDigest string       `json:"previous_state_digest"`
	Queue               []QueueEntry `json:"queue"`
	Active              []Lease      `json:"active"`
	UpdatedAt           time.Time    `json:"updated_at"`
}

// ValidateSchedulerState rejects duplicate PR generations and illegal lease
// bounds before reconciliation.
func ValidateSchedulerState(
	state SchedulerState,
	limits SchedulerLimits,
) error {
	if state.SchemaVersion != 1 || state.Sequence == 0 ||
		!gitSHAPattern.MatchString(state.SourceSHA) ||
		state.UpdatedAt.IsZero() || state.UpdatedAt.Location() != time.UTC ||
		len(state.Queue) > maxSchedulerQueue ||
		len(state.Active) > limits.MaxActive {
		return errors.New("invalid Review scheduler state")
	}
	if state.Sequence == 1 {
		if state.PreviousStateDigest != "" {
			return errors.New(
				"initial Review scheduler state names a predecessor",
			)
		}
	} else if !schedulerDigestPattern.MatchString(
		state.PreviousStateDigest,
	) {
		return errors.New(
			"successor Review scheduler state lacks a predecessor digest",
		)
	}
	seenGenerations := make(map[string]struct{}, len(state.Queue)+len(state.Active))
	activePRs := make(map[int64]int, len(state.Active))
	external := 0
	for _, entry := range state.Queue {
		if err := validateQueueEntry(entry); err != nil {
			return err
		}
		digest := contract.MustGenerationDigest(entry.Generation)
		if _, exists := seenGenerations[digest]; exists {
			return errors.New("duplicate Review scheduler generation")
		}
		seenGenerations[digest] = struct{}{}
	}
	for _, lease := range state.Active {
		if err := validateLease(lease); err != nil {
			return err
		}
		digest := contract.MustGenerationDigest(lease.Generation)
		if _, exists := seenGenerations[digest]; exists {
			return errors.New("duplicate Review scheduler generation")
		}
		seenGenerations[digest] = struct{}{}
		activePRs[lease.Generation.PullRequest]++
		if activePRs[lease.Generation.PullRequest] > limits.MaxPerPullRequest {
			return errors.New("multiple active Review leases for one pull request")
		}
		if lease.FirstTimeExternal {
			external++
		}
	}
	if external > limits.MaxFirstTimeExternal {
		return errors.New("too many first-time external Review leases")
	}
	return nil
}

// CanonicalSchedulerState returns the exact signed scheduler bytes.
func CanonicalSchedulerState(
	state SchedulerState,
	limits SchedulerLimits,
) ([]byte, error) {
	state = normalizeSchedulerCollections(state)
	if err := ValidateSchedulerState(state, limits); err != nil {
		return nil, err
	}
	body, err := json.Marshal(state)
	if err != nil {
		return nil, errors.New("encode Review scheduler state")
	}
	if len(body) > maxSchedulerStateBytes {
		return nil, errors.New(
			"Review scheduler state exceeds canonical byte budget",
		)
	}
	return body, nil
}

// normalizeSchedulerCollections gives semantically empty scheduler
// collections one canonical signed representation.
func normalizeSchedulerCollections(state SchedulerState) SchedulerState {
	if len(state.Queue) == 0 {
		state.Queue = nil
	}
	if len(state.Active) == 0 {
		state.Active = nil
	}
	return state
}

// SchedulerStateDigest identifies one canonical scheduler state.
func SchedulerStateDigest(
	state SchedulerState,
	limits SchedulerLimits,
) (string, error) {
	body, err := CanonicalSchedulerState(state, limits)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// DecodeSchedulerState strictly decodes one bounded signed scheduler state.
func DecodeSchedulerState(
	reader io.Reader,
	maxBytes int64,
	limits SchedulerLimits,
) (SchedulerState, error) {
	if reader == nil || maxBytes <= 0 {
		return SchedulerState{}, errors.New(
			"Review scheduler input limit must be positive",
		)
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return SchedulerState{}, errors.New("read Review scheduler state")
	}
	if int64(len(body)) > maxBytes {
		return SchedulerState{}, errors.New(
			"Review scheduler state exceeds byte limit",
		)
	}
	var state SchedulerState
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return SchedulerState{}, errors.New("decode Review scheduler state")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return SchedulerState{}, errors.New(
			"Review scheduler state contains trailing JSON",
		)
	}
	if err := ValidateSchedulerState(state, limits); err != nil {
		return SchedulerState{}, err
	}
	return state, nil
}

// Enqueue idempotently appends one generation to the FIFO.
func Enqueue(
	state SchedulerState,
	entry QueueEntry,
	limits SchedulerLimits,
) (SchedulerState, error) {
	if err := ValidateSchedulerState(state, limits); err != nil {
		return SchedulerState{}, err
	}
	if err := validateQueueEntry(entry); err != nil {
		return SchedulerState{}, err
	}
	digest := contract.MustGenerationDigest(entry.Generation)
	for _, queued := range state.Queue {
		if contract.MustGenerationDigest(queued.Generation) == digest {
			return state, nil
		}
		if queued.Generation.PullRequest == entry.Generation.PullRequest {
			return SchedulerState{}, errors.New(
				"pull request already has a queued Review generation",
			)
		}
	}
	for _, lease := range state.Active {
		if contract.MustGenerationDigest(lease.Generation) == digest {
			return state, nil
		}
		if lease.Generation.PullRequest == entry.Generation.PullRequest {
			return SchedulerState{}, errors.New(
				"pull request already has an active Review generation",
			)
		}
	}
	if len(state.Queue) == maxSchedulerQueue {
		return SchedulerState{}, errors.New("Review scheduler queue is full")
	}
	previousDigest, err := SchedulerStateDigest(state, limits)
	if err != nil {
		return SchedulerState{}, err
	}
	state.Queue = append(append([]QueueEntry(nil), state.Queue...), entry)
	state.Sequence++
	state.PreviousStateDigest = previousDigest
	state.UpdatedAt = entry.EnqueuedAt
	if _, err := CanonicalSchedulerState(state, limits); err != nil {
		return SchedulerState{}, err
	}
	return state, nil
}

// AcquireNext selects the earliest eligible queue entry without allowing a
// first-time external author to starve ordinary work.
func AcquireNext(
	state SchedulerState,
	runID int64,
	now time.Time,
	limits SchedulerLimits,
) (SchedulerState, *Lease, error) {
	if err := ValidateSchedulerState(state, limits); err != nil {
		return SchedulerState{}, nil, err
	}
	if runID <= 0 || now.IsZero() || now.Location() != time.UTC {
		return SchedulerState{}, nil, errors.New("invalid Review lease authority")
	}
	if len(state.Active) >= limits.MaxActive {
		return state, nil, nil
	}
	externalActive := 0
	for _, lease := range state.Active {
		if lease.FirstTimeExternal {
			externalActive++
		}
	}
	selected := -1
	for index, entry := range state.Queue {
		if entry.FirstTimeExternal &&
			externalActive >= limits.MaxFirstTimeExternal {
			continue
		}
		selected = index
		break
	}
	if selected < 0 {
		return state, nil, nil
	}
	entry := state.Queue[selected]
	lease := Lease{
		Generation:        entry.Generation,
		RunID:             runID,
		FirstTimeExternal: entry.FirstTimeExternal,
		AcquiredAt:        now,
	}
	previousDigest, err := SchedulerStateDigest(state, limits)
	if err != nil {
		return SchedulerState{}, nil, err
	}
	state.Queue = append(
		append([]QueueEntry(nil), state.Queue[:selected]...),
		state.Queue[selected+1:]...,
	)
	state.Active = append(append([]Lease(nil), state.Active...), lease)
	state.Sequence++
	state.PreviousStateDigest = previousDigest
	state.UpdatedAt = now
	return state, &lease, nil
}

// ReleaseLease removes only the lease matching both generation and Actions
// run. Replaying an already completed release is harmless.
func ReleaseLease(
	state SchedulerState,
	generation contract.GenerationIdentity,
	runID int64,
	now time.Time,
	limits SchedulerLimits,
) (SchedulerState, error) {
	if err := ValidateSchedulerState(state, limits); err != nil {
		return SchedulerState{}, err
	}
	if err := contract.ValidateGenerationIdentity(generation); err != nil {
		return SchedulerState{}, err
	}
	if runID <= 0 || now.IsZero() || now.Location() != time.UTC {
		return SchedulerState{}, errors.New("invalid Review lease release")
	}
	targetDigest := contract.MustGenerationDigest(generation)
	for index, lease := range state.Active {
		if lease.Generation.PullRequest != generation.PullRequest {
			continue
		}
		if contract.MustGenerationDigest(lease.Generation) != targetDigest {
			return SchedulerState{}, errors.New(
				"scheduler lease generation does not match",
			)
		}
		if lease.RunID != runID {
			return SchedulerState{}, errors.New(
				"scheduler lease run does not match",
			)
		}
		previousDigest, err := SchedulerStateDigest(state, limits)
		if err != nil {
			return SchedulerState{}, err
		}
		state.Active = append(
			append([]Lease(nil), state.Active[:index]...),
			state.Active[index+1:]...,
		)
		state.Sequence++
		state.PreviousStateDigest = previousDigest
		state.UpdatedAt = now
		return state, nil
	}
	return state, nil
}

func validateQueueEntry(entry QueueEntry) error {
	if err := contract.ValidateGenerationIdentity(entry.Generation); err != nil {
		return err
	}
	if entry.EnqueuedAt.IsZero() || entry.EnqueuedAt.Location() != time.UTC {
		return errors.New("invalid Review queue timestamp")
	}
	return nil
}

func validateLease(lease Lease) error {
	if err := contract.ValidateGenerationIdentity(lease.Generation); err != nil {
		return err
	}
	if lease.RunID <= 0 ||
		lease.AcquiredAt.IsZero() ||
		lease.AcquiredAt.Location() != time.UTC {
		return errors.New("invalid Review lease")
	}
	return nil
}
