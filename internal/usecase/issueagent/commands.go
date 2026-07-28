package issueagent

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

var (
	adoptHeadCommandPattern = regexp.MustCompile(`^/agent adopt-head ([0-9a-f]{40})$`)
	backportCommandPattern  = regexp.MustCompile(`^/agent backport ([A-Za-z0-9][A-Za-z0-9._/-]{0,127})$`)
	recoverCommandPattern   = regexp.MustCompile(`^/agent recover-chain ([1-9][0-9]*) (sha256:[0-9a-f]{64})$`)
)

// CommandKind is one exact maintainer control command.
type CommandKind string

const (
	CommandRevise        CommandKind = "revise"
	CommandCancel        CommandKind = "cancel"
	CommandAddressReview CommandKind = "address_review"
	CommandApproveRisk   CommandKind = "approve_risk"
	CommandAdoptHead     CommandKind = "adopt_head"
	CommandBackport      CommandKind = "backport"
	CommandRecoverChain  CommandKind = "recover_chain"
)

// CommandActor is a fresh GitHub actor permission snapshot.
type CommandActor struct {
	Login      string
	Type       string
	Permission Permission
}

// CommandPolicy contains trusted allowlists for parameterized commands.
type CommandPolicy struct {
	AllowedBackportBranches []string
}

// CommandIntent is a parsed command with no executable text.
type CommandIntent struct {
	Kind                CommandKind
	HeadSHA             string
	BackportBranch      string
	CheckpointCommentID int64
	CheckpointDigest    string
	Actor               string
}

// CommandFacts are freshly re-read GitHub facts needed by parameterized controls.
type CommandFacts struct {
	IssueBodySHA256       string
	AffectedVersion       string
	AcceptedCommentIDs    []int64
	DiagnosisBaseSHA      string
	CommandEventID        string
	CurrentCommentID      int64
	CurrentDigest         string
	UnresolvedThreadIDs   []string
	CurrentExternalHead   string
	MergedPRNumber        int64
	TargetBranch          string
	TargetHeadSHA         string
	LastValidCommentID    int64
	LastValidDigest       string
	QuarantinedCommentIDs []int64
	QuarantineDigest      string
}

// BackportPlan is an independent child Issue seed, never a mutation of main-fix state.
type BackportPlan struct {
	SourceIssue   int64
	SourcePR      int64
	TargetBranch  string
	TargetHeadSHA string
}

// RecoveryPlan is a no-code-write audit-chain recovery request.
type RecoveryPlan struct {
	AnchorCommentID       int64
	AnchorDigest          string
	QuarantinedCommentIDs []int64
	QuarantineDigest      string
}

// CommandPlan freezes the exact effect of one authorized maintainer command.
type CommandPlan struct {
	Kind               CommandKind
	PreviousGeneration uint64
	NewGeneration      uint64
	RevokeLease        bool
	RevisedCheckpoint  *issueagentcontract.Checkpoint
	ReviewThreadIDs    []string
	AdoptedHeadSHA     string
	Backport           *BackportPlan
	Recovery           *RecoveryPlan
}

// ParseCommand accepts only an exact first-line command from an authorized user.
func ParseCommand(
	body string,
	actor CommandActor,
	policy CommandPolicy,
) (CommandIntent, error) {
	if len(body) == 0 || len(body) > 64<<10 {
		return CommandIntent{}, errors.New("command comment is empty or oversized")
	}
	if actor.Type != "User" || actor.Login == "" || !canAuthorize(actor.Permission) {
		return CommandIntent{}, errors.New("actor cannot issue Issue Agent commands")
	}
	firstLine, _, _ := strings.Cut(body, "\n")
	firstLine = strings.TrimSuffix(firstLine, "\r")
	if len(firstLine) > 512 {
		return CommandIntent{}, errors.New("command line is oversized")
	}

	intent := CommandIntent{Actor: actor.Login}
	switch firstLine {
	case "/agent revise":
		intent.Kind = CommandRevise
	case "/agent cancel":
		intent.Kind = CommandCancel
	case "/agent address-review":
		intent.Kind = CommandAddressReview
	case "/agent approve-risk":
		intent.Kind = CommandApproveRisk
	default:
		if matches := adoptHeadCommandPattern.FindStringSubmatch(firstLine); matches != nil {
			intent.Kind = CommandAdoptHead
			intent.HeadSHA = matches[1]
		} else if matches := backportCommandPattern.FindStringSubmatch(firstLine); matches != nil {
			if !slices.Contains(policy.AllowedBackportBranches, matches[1]) {
				return CommandIntent{}, fmt.Errorf("backport branch %q is not allowed", matches[1])
			}
			intent.Kind = CommandBackport
			intent.BackportBranch = matches[1]
		} else if matches := recoverCommandPattern.FindStringSubmatch(firstLine); matches != nil {
			if actor.Permission != PermissionAdmin {
				return CommandIntent{}, errors.New("checkpoint-chain recovery requires admin permission")
			}
			commentID, err := strconv.ParseInt(matches[1], 10, 64)
			if err != nil {
				return CommandIntent{}, errors.New("invalid checkpoint comment ID")
			}
			intent.Kind = CommandRecoverChain
			intent.CheckpointCommentID = commentID
			intent.CheckpointDigest = matches[2]
		} else {
			return CommandIntent{}, errors.New("comment does not begin with an exact Issue Agent command")
		}
	}
	return intent, nil
}

// PlanCommand binds a parsed command to current signed and freshly read facts.
// Every accepted command advances generation, fencing all old Worker results.
func PlanCommand(
	current issueagentcontract.Checkpoint,
	intent CommandIntent,
	facts CommandFacts,
) (CommandPlan, error) {
	if err := issueagentcontract.ValidateCheckpoint(current); err != nil {
		return CommandPlan{}, errors.New("current checkpoint is invalid")
	}
	if intent.Actor == "" || facts.CommandEventID == "" {
		return CommandPlan{}, errors.New("command identity is incomplete")
	}
	plan := CommandPlan{
		Kind: intent.Kind, PreviousGeneration: current.Generation,
		NewGeneration: current.Generation + 1, RevokeLease: current.Lease != nil,
	}
	switch intent.Kind {
	case CommandRevise:
		revised, err := revisedCheckpoint(current, intent, facts)
		if err != nil {
			return CommandPlan{}, err
		}
		plan.RevisedCheckpoint = &revised
	case CommandCancel:
		// Cancellation needs no external parameters; the Publisher appends a
		// terminal, lease-free checkpoint in the new generation.
	case CommandAddressReview:
		if current.Work == nil || current.Work.PRNumber <= 0 ||
			len(facts.UnresolvedThreadIDs) == 0 ||
			!strictCommandStrings(facts.UnresolvedThreadIDs) {
			return CommandPlan{}, errors.New("address-review facts are incomplete")
		}
		plan.ReviewThreadIDs = append([]string(nil), facts.UnresolvedThreadIDs...)
	case CommandApproveRisk:
		if current.State != issueagentcontract.StateDiagnosed ||
			current.Diagnosis == nil || len(current.Diagnosis.RiskClasses) == 0 ||
			current.Diagnosis.AuthorizationEvent != "" ||
			slices.Contains(
				current.Diagnosis.RiskClasses,
				RiskProtectedAgent,
			) {
			return CommandPlan{}, errors.New("risk approval is not applicable")
		}
	case CommandAdoptHead:
		if intent.HeadSHA == "" || intent.HeadSHA != facts.CurrentExternalHead ||
			!fullCommitPattern.MatchString(facts.CurrentExternalHead) ||
			current.Work == nil || facts.CurrentExternalHead == current.Work.HeadSHA {
			return CommandPlan{}, errors.New("adopt-head does not match current external branch head")
		}
		plan.AdoptedHeadSHA = facts.CurrentExternalHead
	case CommandBackport:
		if current.State != issueagentcontract.StateMerged ||
			facts.MergedPRNumber <= 0 ||
			intent.BackportBranch != facts.TargetBranch ||
			!fullCommitPattern.MatchString(facts.TargetHeadSHA) {
			return CommandPlan{}, errors.New("backport requires a merged main fix and exact target head")
		}
		plan.Backport = &BackportPlan{
			SourceIssue: current.IssueNumber, SourcePR: facts.MergedPRNumber,
			TargetBranch: facts.TargetBranch, TargetHeadSHA: facts.TargetHeadSHA,
		}
	case CommandRecoverChain:
		if intent.CheckpointCommentID != facts.LastValidCommentID ||
			intent.CheckpointDigest != facts.LastValidDigest ||
			!strictPositiveIDs(facts.QuarantinedCommentIDs) ||
			!scheduleDigestPattern.MatchString(facts.QuarantineDigest) {
			return CommandPlan{}, errors.New("recover-chain does not match the audited anchor")
		}
		plan.Recovery = &RecoveryPlan{
			AnchorCommentID: facts.LastValidCommentID,
			AnchorDigest:    facts.LastValidDigest,
			QuarantinedCommentIDs: append(
				[]int64(nil), facts.QuarantinedCommentIDs...,
			),
			QuarantineDigest: facts.QuarantineDigest,
		}
	default:
		return CommandPlan{}, errors.New("unsupported Issue Agent command")
	}
	return plan, nil
}

func revisedCheckpoint(
	current issueagentcontract.Checkpoint,
	intent CommandIntent,
	facts CommandFacts,
) (issueagentcontract.Checkpoint, error) {
	next := issueagentcontract.Checkpoint{
		SchemaVersion: 1, Repository: current.Repository,
		IssueNumber: current.IssueNumber, Generation: current.Generation + 1,
		Sequence: current.Sequence + 1,
		State:    issueagentcontract.StateAuthorized,
		FrozenInput: issueagentcontract.FrozenInput{
			IssueBodySHA256:    facts.IssueBodySHA256,
			AffectedVersion:    facts.AffectedVersion,
			AcceptedCommentIDs: append([]int64(nil), facts.AcceptedCommentIDs...),
			AuthorizationEvent: facts.CommandEventID,
			AuthorizedBy:       intent.Actor,
		},
		Versions: issueagentcontract.Versions{
			ReportedRef:      facts.AffectedVersion,
			DiagnosisBaseSHA: facts.DiagnosisBaseSHA,
		},
		NextAction: issueagentcontract.ActionPinVersions,
	}
	if facts.CurrentCommentID <= 0 ||
		!scheduleDigestPattern.MatchString(facts.CurrentDigest) {
		return issueagentcontract.Checkpoint{}, errors.New("revise predecessor is invalid")
	}
	next.ExpectedPreviousCheckpointID = &facts.CurrentCommentID
	next.PreviousCheckpointSHA256 = &facts.CurrentDigest
	if err := issueagentcontract.ValidateCheckpoint(next); err != nil {
		return issueagentcontract.Checkpoint{}, err
	}
	return next, nil
}

func strictCommandStrings(values []string) bool {
	if len(values) > 128 || !slices.IsSorted(values) {
		return false
	}
	for index, value := range values {
		if value == "" || len(value) > 256 ||
			index > 0 && values[index-1] == value {
			return false
		}
	}
	return true
}

func strictPositiveIDs(values []int64) bool {
	if len(values) == 0 || len(values) > 128 || !slices.IsSorted(values) {
		return false
	}
	for index, value := range values {
		if value <= 0 || index > 0 && values[index-1] == value {
			return false
		}
	}
	return true
}
