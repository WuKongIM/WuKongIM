package issueagent

import (
	"errors"
	"slices"
	"time"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// Permission is the current repository permission returned by GitHub.
type Permission string

const (
	PermissionNone     Permission = "none"
	PermissionRead     Permission = "read"
	PermissionTriage   Permission = "triage"
	PermissionWrite    Permission = "write"
	PermissionMaintain Permission = "maintain"
	PermissionAdmin    Permission = "admin"
)

// AuthorizationFacts are current GitHub facts for one label event.
type AuthorizationFacts struct {
	Repository          string
	IssueNumber         int64
	EventID             string
	EventAction         string
	Label               string
	BeforeLabels        []string
	AfterLabels         []string
	Actor               string
	ActorType           string
	Permission          Permission
	EventAt             time.Time
	PermissionCheckedAt time.Time
	IssueBodySHA256     string
	AffectedVersion     string
	AcceptedCommentIDs  []int64
	DiagnosisBaseSHA    string
}

// Authorize creates the first complete checkpoint from one exact maintainer event.
func Authorize(
	facts AuthorizationFacts,
	now time.Time,
	maxPermissionAge time.Duration,
) (issueagentcontract.Checkpoint, error) {
	if now.IsZero() || maxPermissionAge <= 0 {
		return issueagentcontract.Checkpoint{}, errors.New("authorization clock policy is invalid")
	}
	if facts.EventAction != "labeled" ||
		facts.Label != "ready-for-agent" ||
		slices.Contains(facts.BeforeLabels, "ready-for-agent") ||
		!slices.Contains(facts.AfterLabels, "ready-for-agent") {
		return issueagentcontract.Checkpoint{}, errors.New("event did not newly add ready-for-agent")
	}
	if facts.ActorType != "User" || !canAuthorize(facts.Permission) {
		return issueagentcontract.Checkpoint{}, errors.New("actor cannot authorize Issue Agent execution")
	}
	if facts.EventAt.IsZero() ||
		facts.PermissionCheckedAt.Before(facts.EventAt) ||
		facts.PermissionCheckedAt.After(now) ||
		now.Sub(facts.PermissionCheckedAt) > maxPermissionAge {
		return issueagentcontract.Checkpoint{}, errors.New("repository permission snapshot is stale")
	}

	checkpoint := issueagentcontract.Checkpoint{
		SchemaVersion: 1,
		Repository:    facts.Repository,
		IssueNumber:   facts.IssueNumber,
		Generation:    1,
		Sequence:      1,
		State:         issueagentcontract.StateAuthorized,
		FrozenInput: issueagentcontract.FrozenInput{
			IssueBodySHA256:    facts.IssueBodySHA256,
			AffectedVersion:    facts.AffectedVersion,
			AcceptedCommentIDs: append([]int64(nil), facts.AcceptedCommentIDs...),
			AuthorizationEvent: facts.EventID,
			AuthorizedBy:       facts.Actor,
		},
		Versions: issueagentcontract.Versions{
			ReportedRef:      facts.AffectedVersion,
			DiagnosisBaseSHA: facts.DiagnosisBaseSHA,
		},
		Budget:     issueagentcontract.Budget{},
		NextAction: issueagentcontract.ActionPinVersions,
	}
	if err := issueagentcontract.ValidateCheckpoint(checkpoint); err != nil {
		return issueagentcontract.Checkpoint{}, err
	}
	return checkpoint, nil
}

func canAuthorize(permission Permission) bool {
	switch permission {
	case PermissionWrite, PermissionMaintain, PermissionAdmin:
		return true
	default:
		return false
	}
}
