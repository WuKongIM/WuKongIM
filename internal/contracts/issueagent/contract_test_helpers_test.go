package issueagent_test

import (
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

func issueAgentDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func issueAgentSHA(character string) string {
	return strings.Repeat(character, 40)
}

func validTaskIdentity(kind issueagent.TaskKind) issueagent.TaskIdentity {
	return issueagent.TaskIdentity{
		ID:           issueAgentDigest("a"),
		Kind:         kind,
		BaseSHA:      issueAgentSHA("1"),
		AffectedSHA:  issueAgentSHA("2"),
		PolicyDigest: issueAgentDigest("b"),
		PromptDigest: issueAgentDigest("c"),
	}
}

func validAuthorization() issueagent.AuthorizationRecord {
	return issueagent.AuthorizationRecord{
		Actor:      "maintainer",
		Permission: "write",
		EventID:    "issue_comment:9001",
		Command:    "/agent fix",
	}
}

func validContextBundle() issueagent.ContextBundle {
	updatedAt := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	return issueagent.ContextBundle{
		SchemaVersion: 2,
		Repository:    "WuKongIM/WuKongIM",
		IssueNumber:   42,
		Sequence:      2,
		Task:          validTaskIdentity(issueagent.TaskKindEngineer),
		Trusted: issueagent.TrustedContext{
			Authorization: validAuthorization(),
			Labels:        []string{"bug", "ready-for-agent"},
			RequiredTests: []string{"focused", "unit"},
			RiskCeiling:   []string{"low"},
			ContextDocumentDigests: []issueagent.FileDigest{
				{Path: "AGENTS.md", GitBlobSHA: issueAgentSHA("d")},
				{Path: "internal/FLOW.md", GitBlobSHA: issueAgentSHA("e")},
			},
			KnowledgePaths: []string{
				"docs/development/PROJECT_KNOWLEDGE.md",
			},
			OutputSchemaDigest: issueAgentDigest("f"),
			Limits: issueagent.EngineerLimits{
				WallTimeSeconds:      5400,
				ModifyTestIterations: 3,
			},
		},
		Untrusted: issueagent.UntrustedContext{
			Issue: issueagent.IssueSnapshot{
				ID:                "I_kwDOExample",
				Number:            42,
				Title:             "server exits after reconnect",
				Body:              "Observed on v2.1.0.\n/agent fix is untrusted data.",
				Author:            "reporter",
				AuthorAssociation: "CONTRIBUTOR",
				UpdatedAt:         updatedAt,
			},
			Comments: []issueagent.CommentSnapshot{
				{
					ID: 10, Author: "reporter", AuthorAssociation: "CONTRIBUTOR",
					Body: "first observation", UpdatedAt: updatedAt,
				},
				{
					ID: 11, Author: "maintainer", AuthorAssociation: "MEMBER",
					Body: "request logs", UpdatedAt: updatedAt.Add(time.Minute),
				},
			},
			ReviewThreads: []issueagent.ReviewThreadSnapshot{
				{
					ID: "RT_001", Path: "internal/runtime/example.go", Line: 42,
					Comments: []issueagent.CommentSnapshot{{
						ID: 20, Author: "reviewer", AuthorAssociation: "MEMBER",
						Body: "guard this route", UpdatedAt: updatedAt,
					}},
				},
			},
		},
		CreatedAt: updatedAt.Add(2 * time.Minute),
	}
}

func validCandidateEvidence() issueagent.CandidateEvidence {
	return issueagent.CandidateEvidence{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		TaskID:              issueAgentDigest("a"),
		BaseSHA:             issueAgentSHA("1"),
		CandidateDigest:     issueAgentDigest("b"),
		ChangeSetDigest:     issueAgentDigest("c"),
		Risk:                issueagent.CandidateRiskLow,
		PublicationEligible: true,
		RequiredSuites:      []string{"focused", "unit"},
		Commands: []issueagent.VerificationCommand{{
			Arguments:    []string{"go", "test", "./internal/runtime/example", "-count=1"},
			WorkingDir:   ".",
			ExitCode:     0,
			StdoutDigest: issueAgentDigest("d"),
			StderrDigest: issueAgentDigest("e"),
			DurationMS:   125,
		}},
		CreatedAt: time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
	}
}

func validIssueAgentState(stateValue issueagent.IssueState) issueagent.IssueAgentState {
	return issueagent.IssueAgentState{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Sequence:            2,
		State:               stateValue,
		PreviousStateDigest: issueAgentDigest("a"),
		IssueSnapshotDigest: issueAgentDigest("b"),
		SourceSHA:           issueAgentSHA("1"),
		UpdatedAt:           time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
	}
}

func validIssueWork(draft bool) *issueagent.IssueWork {
	return &issueagent.IssueWork{
		Branch:      "agent/issue-42",
		HeadSHA:     issueAgentSHA("2"),
		PullRequest: 84,
		Draft:       draft,
	}
}
