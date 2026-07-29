package issueagent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// ImageSource identifies source metadata attached to one immutable image digest.
type ImageSource struct {
	SourceSHA string
	Verified  bool
}

// SourceResolver is the narrow read-only port used to pin a reported version.
// A tag returns every commit candidate after annotated-tag dereferencing so
// ambiguity is rejected rather than guessed.
type SourceResolver interface {
	CommitExists(context.Context, string) (bool, error)
	ResolveTag(context.Context, string) ([]string, error)
	ResolveImageDigest(context.Context, string) (ImageSource, error)
}

// ResolveVersions converts an immutable reported reference and authorization
// baseline into exact source SHAs.
func ResolveVersions(
	ctx context.Context,
	resolver SourceResolver,
	reportedRef string,
	diagnosisBaseSHA string,
) (issueagentcontract.Versions, error) {
	if ctx == nil || resolver == nil ||
		!fullCommitPattern.MatchString(diagnosisBaseSHA) {
		return issueagentcontract.Versions{}, errors.New("version resolution input is invalid")
	}
	baseExists, err := resolver.CommitExists(ctx, diagnosisBaseSHA)
	if err != nil {
		return issueagentcontract.Versions{}, errors.New("resolve diagnosis baseline")
	}
	if !baseExists {
		return issueagentcontract.Versions{}, errors.New("diagnosis baseline commit is missing")
	}

	reportedRef = strings.TrimSpace(reportedRef)
	var affectedSHA string
	switch {
	case fullCommitPattern.MatchString(reportedRef):
		exists, err := resolver.CommitExists(ctx, strings.ToLower(reportedRef))
		if err != nil {
			return issueagentcontract.Versions{}, errors.New("resolve affected commit")
		}
		if !exists {
			return issueagentcontract.Versions{}, errors.New("affected commit is missing")
		}
		affectedSHA = strings.ToLower(reportedRef)
	case semverTagPattern.MatchString(reportedRef):
		candidates, err := resolver.ResolveTag(ctx, reportedRef)
		if err != nil {
			return issueagentcontract.Versions{}, errors.New("resolve affected release tag")
		}
		affectedSHA, err = oneCommitCandidate(candidates)
		if err != nil {
			return issueagentcontract.Versions{}, err
		}
	case imageDigestPattern.MatchString(reportedRef):
		source, err := resolver.ResolveImageDigest(ctx, reportedRef)
		if err != nil {
			return issueagentcontract.Versions{}, errors.New("resolve affected image metadata")
		}
		if !source.Verified || !fullCommitPattern.MatchString(source.SourceSHA) {
			return issueagentcontract.Versions{},
				errors.New("image digest lacks verified source-SHA metadata")
		}
		exists, err := resolver.CommitExists(ctx, source.SourceSHA)
		if err != nil {
			return issueagentcontract.Versions{}, errors.New("verify image source commit")
		}
		if !exists {
			return issueagentcontract.Versions{}, errors.New("image source commit is missing")
		}
		affectedSHA = source.SourceSHA
	default:
		return issueagentcontract.Versions{},
			fmt.Errorf("reported version %q is moving or unsupported", reportedRef)
	}
	return issueagentcontract.Versions{
		ReportedRef:      reportedRef,
		AffectedSHA:      strings.ToLower(affectedSHA),
		DiagnosisBaseSHA: strings.ToLower(diagnosisBaseSHA),
	}, nil
}

func oneCommitCandidate(candidates []string) (string, error) {
	unique := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.ToLower(candidate)
		if !fullCommitPattern.MatchString(candidate) {
			return "", errors.New("release tag resolved to a non-commit object")
		}
		unique[candidate] = struct{}{}
	}
	if len(unique) != 1 {
		return "", errors.New("release tag is missing or ambiguous")
	}
	for candidate := range unique {
		return candidate, nil
	}
	panic("unreachable")
}
