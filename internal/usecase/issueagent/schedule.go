package issueagent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

var scheduleDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Candidate is one Issue eligible for a new Worker lease.
type Candidate struct {
	Repository   string
	IssueNumber  int64
	Generation   uint64
	NextSequence uint64
	Phase        issueagentcontract.Phase
	TaskDigest   string
	EligibleAt   time.Time
	Timeout      time.Duration
	Reserved     time.Duration
	Heavy        bool
	PriorityHigh bool
}

// ActiveLease is current signed capacity derived from an Issue checkpoint.
type ActiveLease struct {
	IssueNumber int64
	Heavy       bool
	ExpiresAt   time.Time
}

// WorkerStart is rolling repository usage cross-checked with Actions metadata.
type WorkerStart struct {
	StartedAt time.Time
	Reserved  time.Duration
}

// LeasePlan is one deterministic proposal for the trusted Publisher.
type LeasePlan struct {
	Repository  string
	IssueNumber int64
	Generation  uint64
	Sequence    uint64
	Phase       issueagentcontract.Phase
	OperationID string
	TaskDigest  string
	IssuedAt    time.Time
	ExpiresAt   time.Time
	Reserved    time.Duration
	Heavy       bool
}

// Schedule allocates bounded repository capacity without mutating Issue state.
func Schedule(
	now time.Time,
	candidates []Candidate,
	active []ActiveLease,
	starts []WorkerStart,
	budget RepositoryBudget,
	leaseMargin time.Duration,
) ([]LeasePlan, error) {
	if now.IsZero() || leaseMargin <= 0 ||
		budget.MaxActiveWorkers <= 0 || budget.MaxActiveWorkers > 3 ||
		budget.MaxHeavyWorkers <= 0 || budget.MaxHeavyWorkers > 1 ||
		budget.RollingWindow != 24*time.Hour ||
		budget.MaxStartedWorkerTime <= 0 ||
		budget.MaxStartedWorkerTime > 24*time.Hour {
		return nil, errors.New("scheduler policy is invalid")
	}

	activeIssues := make(map[int64]struct{}, len(active))
	activeCount := 0
	activeHeavy := 0
	for _, lease := range active {
		if lease.IssueNumber <= 0 || lease.ExpiresAt.IsZero() {
			return nil, errors.New("active lease is invalid")
		}
		if _, duplicate := activeIssues[lease.IssueNumber]; duplicate {
			return nil, errors.New("duplicate active Issue lease")
		}
		activeIssues[lease.IssueNumber] = struct{}{}
		if !lease.ExpiresAt.After(now) {
			continue
		}
		activeCount++
		if lease.Heavy {
			activeHeavy++
		}
	}
	if activeCount >= budget.MaxActiveWorkers {
		return []LeasePlan{}, nil
	}

	windowStart := now.Add(-budget.RollingWindow)
	var rollingUse time.Duration
	for _, start := range starts {
		if start.StartedAt.IsZero() || start.StartedAt.After(now) || start.Reserved <= 0 {
			return nil, errors.New("Worker start accounting is invalid")
		}
		if !start.StartedAt.Before(windowStart) {
			rollingUse += start.Reserved
		}
	}
	if rollingUse >= budget.MaxStartedWorkerTime {
		return []LeasePlan{}, nil
	}

	ordered := append([]Candidate(nil), candidates...)
	seenCandidates := make(map[int64]struct{}, len(ordered))
	for _, candidate := range ordered {
		if err := validateCandidate(candidate, now); err != nil {
			return nil, err
		}
		if _, duplicate := seenCandidates[candidate.IssueNumber]; duplicate {
			return nil, fmt.Errorf("duplicate candidate for Issue %d", candidate.IssueNumber)
		}
		seenCandidates[candidate.IssueNumber] = struct{}{}
	}
	slices.SortStableFunc(ordered, func(left, right Candidate) int {
		if left.PriorityHigh != right.PriorityHigh {
			if left.PriorityHigh {
				return -1
			}
			return 1
		}
		if comparison := left.EligibleAt.Compare(right.EligibleAt); comparison != 0 {
			return comparison
		}
		return int(left.IssueNumber - right.IssueNumber)
	})

	plans := make([]LeasePlan, 0, budget.MaxActiveWorkers-activeCount)
	for _, candidate := range ordered {
		if activeCount >= budget.MaxActiveWorkers {
			break
		}
		if _, alreadyActive := activeIssues[candidate.IssueNumber]; alreadyActive {
			continue
		}
		if candidate.Heavy && activeHeavy >= budget.MaxHeavyWorkers {
			continue
		}
		if rollingUse+candidate.Reserved > budget.MaxStartedWorkerTime {
			continue
		}
		operationID := operationID(candidate)
		plans = append(plans, LeasePlan{
			Repository:  candidate.Repository,
			IssueNumber: candidate.IssueNumber,
			Generation:  candidate.Generation,
			Sequence:    candidate.NextSequence,
			Phase:       candidate.Phase,
			OperationID: operationID,
			TaskDigest:  candidate.TaskDigest,
			IssuedAt:    now,
			ExpiresAt:   now.Add(candidate.Timeout + leaseMargin),
			Reserved:    candidate.Reserved,
			Heavy:       candidate.Heavy,
		})
		activeIssues[candidate.IssueNumber] = struct{}{}
		activeCount++
		rollingUse += candidate.Reserved
		if candidate.Heavy {
			activeHeavy++
		}
	}
	return plans, nil
}

func validateCandidate(candidate Candidate, now time.Time) error {
	if candidate.Repository == "" ||
		candidate.IssueNumber <= 0 ||
		candidate.Generation == 0 ||
		candidate.NextSequence == 0 ||
		candidate.EligibleAt.IsZero() ||
		candidate.EligibleAt.After(now) ||
		candidate.Timeout <= 0 ||
		candidate.Timeout > 2*time.Hour ||
		candidate.Reserved < candidate.Timeout ||
		candidate.Reserved > 2*time.Hour ||
		!scheduleDigestPattern.MatchString(candidate.TaskDigest) {
		return fmt.Errorf("candidate Issue %d is invalid", candidate.IssueNumber)
	}
	switch candidate.Phase {
	case issueagentcontract.PhaseReproduce,
		issueagentcontract.PhaseDiagnose,
		issueagentcontract.PhaseFix,
		issueagentcontract.PhaseAddressReview:
		return nil
	default:
		return fmt.Errorf("candidate Issue %d has invalid phase", candidate.IssueNumber)
	}
}

func operationID(candidate Candidate) string {
	hasher := sha256.New()
	parts := []string{
		candidate.Repository,
		strconv.FormatInt(candidate.IssueNumber, 10),
		strconv.FormatUint(candidate.Generation, 10),
		strconv.FormatUint(candidate.NextSequence, 10),
		string(candidate.Phase),
		candidate.TaskDigest,
	}
	for _, part := range parts {
		hasher.Write([]byte(part))
		hasher.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}
