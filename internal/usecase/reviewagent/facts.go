package reviewagent

import (
	"errors"
	"slices"
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

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
	ChangedFiles          int
	ChangedBytes          int64
	ChangedLines          int64
	AuthorLogin           string
	AuthorAssociation     string
	ControlPlaneChanged   bool
	OwnerApproved         bool
	HumanChangesRequested bool
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
	testMergeSHA := facts.TestMergeSHA
	if testMergeSHA == "" {
		testMergeSHA = strings.Repeat("0", 40)
	}
	return contract.GenerationIdentity{
		Repository:     facts.Repository,
		PullRequest:    facts.PullRequest,
		HeadSHA:        facts.HeadSHA,
		BaseSHA:        facts.BaseSHA,
		TestMergeSHA:   testMergeSHA,
		IntentDigest:   facts.IntentDigest,
		Generation:     number,
		StateParentSHA: facts.StateParentSHA,
	}
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
