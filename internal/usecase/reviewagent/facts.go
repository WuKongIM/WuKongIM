package reviewagent

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

var gitSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Mergeability is the normalized fresh GitHub mergeability fact.
type Mergeability string

const (
	MergeabilityClean       Mergeability = "clean"
	MergeabilityConflicting Mergeability = "conflicting"
	MergeabilityUnknown     Mergeability = "unknown"
)

// PullRequestFacts contains only fresh, complete facts needed by lifecycle
// policy. Event payload content is never authoritative here.
type PullRequestFacts struct {
	Repository            string
	PullRequest           int64
	BaseRef               string
	HeadSHA               string
	BaseSHA               string
	TestMergeSHA          string
	IntentDigest          string
	StateParentSHA        string
	Open                  bool
	Draft                 bool
	Mergeability          Mergeability
	ContextFailureReason  string
	ChangedFiles          int
	ChangedBytes          int64
	ChangedLines          int64
	AuthorLogin           string
	AuthorAssociation     string
	HumanChangesRequested bool
}

// ReviewFact is the normalized portion of a formal Review needed for
// deterministic human-blocking policy.
type ReviewFact struct {
	Author      string
	AuthorType  string
	State       string
	CommitSHA   string
	SubmittedAt time.Time
}

// SchedulerLimits are the protected repository-wide lease bounds.
type SchedulerLimits struct {
	MaxActive            int
	MaxPerPullRequest    int
	MaxFirstTimeExternal int
}

// Policy is the lifecycle-relevant projection of protected policy.json.
type Policy struct {
	SupportedBaseBranches         []string
	MaxChangedFiles               int
	MaxChangedBytes               int64
	MaxChangedLines               int64
	MaxGenerationDuration         time.Duration
	MaxAutomaticReviewsPerHead    int
	MaxReconsiderationsPerHead    int
	MaxInfrastructureRetries      int
	MaxExplanationSessionsPerHead int
	MaxExplanationResponseBytes   int
	Scheduler                     SchedulerLimits
}

func validatePolicy(policy Policy) error {
	if len(policy.SupportedBaseBranches) == 0 ||
		policy.MaxChangedFiles <= 0 ||
		policy.MaxChangedBytes <= 0 ||
		policy.MaxChangedLines <= 0 ||
		policy.MaxGenerationDuration <= 0 ||
		policy.MaxAutomaticReviewsPerHead <= 0 ||
		policy.MaxReconsiderationsPerHead < 0 ||
		policy.MaxInfrastructureRetries < 0 ||
		policy.MaxExplanationSessionsPerHead <= 0 ||
		policy.MaxExplanationResponseBytes <= 0 ||
		policy.Scheduler.MaxActive <= 0 ||
		policy.Scheduler.MaxPerPullRequest != 1 ||
		policy.Scheduler.MaxFirstTimeExternal <= 0 ||
		policy.Scheduler.MaxFirstTimeExternal >
			policy.Scheduler.MaxActive {
		return errors.New("invalid Review Agent lifecycle policy")
	}
	for _, branch := range policy.SupportedBaseBranches {
		if strings.TrimSpace(branch) == "" {
			return errors.New("invalid Review Agent base branch")
		}
	}
	return nil
}

func generationFromFacts(
	facts PullRequestFacts,
	number uint64,
) contract.GenerationIdentity {
	return contract.GenerationIdentity{
		Repository:     facts.Repository,
		PullRequest:    facts.PullRequest,
		HeadSHA:        facts.HeadSHA,
		BaseSHA:        facts.BaseSHA,
		TestMergeSHA:   NormalizeTestMergeSHA(facts.TestMergeSHA),
		IntentDigest:   facts.IntentDigest,
		Generation:     number,
		StateParentSHA: facts.StateParentSHA,
	}
}

// NormalizeTestMergeSHA gives an unavailable GitHub test-merge revision one
// stable generation coordinate without treating it as reviewable evidence.
func NormalizeTestMergeSHA(value string) string {
	if value == "" {
		return strings.Repeat("0", 40)
	}
	return value
}

func supportedBase(policy Policy, base string) bool {
	return slices.Contains(policy.SupportedBaseBranches, base)
}

func firstTimeExternal(association string) bool {
	switch association {
	case "FIRST_TIME_CONTRIBUTOR", "FIRST_TIMER", "NONE":
		return true
	default:
		return false
	}
}

// HumanChangesRequested reports whether the latest exact-head Review from any
// human reviewer still requests changes.
func HumanChangesRequested(
	reviews []ReviewFact,
	headSHA string,
) bool {
	latest := make(map[string]ReviewFact)
	for _, review := range reviews {
		if review.AuthorType != "User" || review.CommitSHA != headSHA {
			continue
		}
		author := strings.ToLower(review.Author)
		current, exists := latest[author]
		if !exists || review.SubmittedAt.After(current.SubmittedAt) {
			latest[author] = review
		}
	}
	for _, review := range latest {
		if review.State == "CHANGES_REQUESTED" {
			return true
		}
	}
	return false
}
