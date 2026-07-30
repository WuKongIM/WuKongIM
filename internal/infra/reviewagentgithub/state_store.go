package reviewagentgithub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

var appBotLoginPattern = regexp.MustCompile(
	`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})\[bot\]$`,
)

// StateCommitRequest is one exact expected-head state append.
type StateCommitRequest struct {
	Branch            string
	Path              string
	ExpectedParentSHA string
	ExistingBranch    bool
	Message           string
	Content           []byte
}

// StateCommitResult is the independently re-read signed commit identity.
type StateCommitResult struct {
	CommitSHA      string
	ParentSHA      string
	Path           string
	ContentDigest  string
	AuthorLogin    string
	AuthorType     string
	Verified       bool
	SignedByGitHub bool
}

// StateCommitRecord is one complete re-read state commit.
type StateCommitRecord struct {
	CommitSHA      string
	ParentSHA      string
	Message        string
	Path           string
	Content        []byte
	AuthorLogin    string
	AuthorType     string
	Verified       bool
	SignedByGitHub bool
}

// StateCommitPort is the narrow GitHub signed-state boundary.
type StateCommitPort interface {
	PublishStateCommit(context.Context, StateCommitRequest) (StateCommitResult, error)
	StateRefHead(context.Context, string) (string, bool, error)
	ReadStateCommit(context.Context, string, string) (StateCommitRecord, error)
}

// ReviewStateStore owns canonical per-PR state refs.
type ReviewStateStore struct {
	repository string
	appLogin   string
	commits    StateCommitPort
}

// LoadedReviewState is the latest verified state and exact ref head.
type LoadedReviewState struct {
	HeadSHA string
	State   contract.ReviewState
}

// NewReviewStateStore binds state access to one repository and State Writer.
func NewReviewStateStore(
	repository string,
	appLogin string,
	commits StateCommitPort,
) (*ReviewStateStore, error) {
	if !repositoryPattern.MatchString(repository) ||
		!appBotLoginPattern.MatchString(appLogin) ||
		commits == nil {
		return nil, errors.New("Review State Store configuration is invalid")
	}
	return &ReviewStateStore{
		repository: repository,
		appLogin:   appLogin,
		commits:    commits,
	}, nil
}

// Load verifies the current App-signed rolling checkpoint and its immediate
// predecessor. Older commits remain append-only audit history without making
// the controller's hot path grow with the age of a pull request.
func (store *ReviewStateStore) Load(
	ctx context.Context,
	pullRequest int64,
) (LoadedReviewState, bool, error) {
	if store == nil || ctx == nil || pullRequest <= 0 {
		return LoadedReviewState{}, false, errors.New(
			"Review State Store load request is invalid",
		)
	}
	branch, path := pullRequestStateTarget(pullRequest)
	head, found, err := store.commits.StateRefHead(ctx, branch)
	if err != nil || !found {
		return LoadedReviewState{}, found, err
	}
	if !gitSHAPattern.MatchString(head) {
		return LoadedReviewState{}, false, errors.New("Review state head is invalid")
	}

	latestRecord, latest, err := store.readCheckpoint(
		ctx,
		head,
		path,
		pullRequest,
	)
	if err != nil {
		return LoadedReviewState{}, false, err
	}
	if latest.Sequence == 1 {
		if latestRecord.ParentSHA != latest.Generation.StateParentSHA {
			return LoadedReviewState{}, false, errors.New(
				"initial Review state has the wrong source parent",
			)
		}
		return LoadedReviewState{HeadSHA: head, State: latest}, true, nil
	}
	if !gitSHAPattern.MatchString(latestRecord.ParentSHA) {
		return LoadedReviewState{}, false, errors.New(
			"Review state predecessor is invalid",
		)
	}
	_, previous, err := store.readCheckpoint(
		ctx,
		latestRecord.ParentSHA,
		path,
		pullRequest,
	)
	if err != nil {
		return LoadedReviewState{}, false, err
	}
	digest, err := contract.ReviewStateDigest(previous)
	if err != nil ||
		latest.PreviousStateDigest != digest ||
		previous.Sequence+1 != latest.Sequence ||
		previous.UpdatedAt.After(latest.UpdatedAt) {
		return LoadedReviewState{}, false, errors.New(
			"Review state rolling checkpoint is not contiguous",
		)
	}
	return LoadedReviewState{HeadSHA: head, State: latest}, true, nil
}

func (store *ReviewStateStore) readCheckpoint(
	ctx context.Context,
	commitSHA string,
	path string,
	pullRequest int64,
) (StateCommitRecord, contract.ReviewState, error) {
	record, err := store.commits.ReadStateCommit(ctx, commitSHA, path)
	if err != nil {
		return StateCommitRecord{}, contract.ReviewState{}, err
	}
	state, err := store.validateRecord(
		record,
		commitSHA,
		path,
		pullRequest,
	)
	if err != nil {
		return StateCommitRecord{}, contract.ReviewState{}, err
	}
	return record, state, nil
}

// Advance appends one canonical per-PR state at the exact expected head.
func (store *ReviewStateStore) Advance(
	ctx context.Context,
	state contract.ReviewState,
	expectedParentSHA string,
	existingBranch bool,
) (string, error) {
	if store == nil || ctx == nil ||
		state.Generation.Repository != store.repository ||
		!gitSHAPattern.MatchString(expectedParentSHA) ||
		(!existingBranch &&
			(state.Sequence != 1 ||
				expectedParentSHA != state.Generation.StateParentSHA)) ||
		(existingBranch && state.Sequence == 1) {
		return "", errors.New("Review State Store advance request is invalid")
	}
	content, err := contract.CanonicalReviewState(state)
	if err != nil {
		return "", err
	}
	branch, path := pullRequestStateTarget(state.Generation.PullRequest)
	message := fmt.Sprintf(
		"review(state): pr %d sequence %d",
		state.Generation.PullRequest,
		state.Sequence,
	)
	result, err := store.commits.PublishStateCommit(ctx, StateCommitRequest{
		Branch: branch, Path: path,
		ExpectedParentSHA: expectedParentSHA,
		ExistingBranch:    existingBranch,
		Message:           message,
		Content:           content,
	})
	if err != nil {
		return "", err
	}
	if err := validatePublishedState(
		result,
		expectedParentSHA,
		path,
		content,
		store.appLogin,
	); err != nil {
		return "", err
	}
	return result.CommitSHA, nil
}

func (store *ReviewStateStore) validateRecord(
	record StateCommitRecord,
	expectedCommitSHA string,
	expectedPath string,
	pullRequest int64,
) (contract.ReviewState, error) {
	if err := validateStateRecordTrust(
		record,
		expectedCommitSHA,
		expectedPath,
		store.appLogin,
	); err != nil {
		return contract.ReviewState{}, err
	}
	state, err := contract.DecodeReviewState(
		bytes.NewReader(record.Content),
		contract.MaxReviewStateBytes,
	)
	if err != nil ||
		state.Generation.Repository != store.repository ||
		state.Generation.PullRequest != pullRequest ||
		record.Message != fmt.Sprintf(
			"review(state): pr %d sequence %d",
			pullRequest,
			state.Sequence,
		) {
		return contract.ReviewState{}, errors.New(
			"Review state commit content is invalid",
		)
	}
	canonical, err := contract.CanonicalReviewState(state)
	if err != nil || !bytes.Equal(canonical, record.Content) {
		return contract.ReviewState{}, errors.New(
			"Review state commit is not canonical",
		)
	}
	return state, nil
}

func pullRequestStateTarget(pullRequest int64) (string, string) {
	return fmt.Sprintf("review-state/pr-%d", pullRequest),
		fmt.Sprintf(".review-agent-state/pr-%d.json", pullRequest)
}

func validateStateRecordTrust(
	record StateCommitRecord,
	expectedCommitSHA string,
	expectedPath string,
	appLogin string,
) error {
	if record.CommitSHA != expectedCommitSHA ||
		!gitSHAPattern.MatchString(record.ParentSHA) ||
		record.Path != expectedPath ||
		record.AuthorLogin != appLogin ||
		record.AuthorType != "Bot" ||
		!record.Verified ||
		!record.SignedByGitHub {
		return errors.New("Review state commit is untrusted")
	}
	return nil
}

func validatePublishedState(
	result StateCommitResult,
	expectedParentSHA string,
	expectedPath string,
	content []byte,
	appLogin string,
) error {
	sum := sha256.Sum256(content)
	if !gitSHAPattern.MatchString(result.CommitSHA) ||
		result.ParentSHA != expectedParentSHA ||
		result.Path != expectedPath ||
		result.ContentDigest != "sha256:"+hex.EncodeToString(sum[:]) ||
		result.AuthorLogin != appLogin ||
		result.AuthorType != "Bot" ||
		!result.Verified ||
		!result.SignedByGitHub {
		return errors.New("published Review state commit is untrusted")
	}
	return nil
}
