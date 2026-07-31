package reviewagentgithub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

const (
	schedulerStateBranch = "review-state/scheduler"
	schedulerStatePath   = ".review-agent-state/scheduler.json"
)

// SchedulerStore owns the one canonical repository-wide scheduler ref.
type SchedulerStore struct {
	appLogin string
	commits  StateCommitPort
	limits   usecase.SchedulerLimits
}

// LoadedSchedulerState is the latest verified scheduler and exact ref head.
type LoadedSchedulerState struct {
	HeadSHA string
	State   usecase.SchedulerState
}

// NewSchedulerStore binds scheduling state to the exact protected target.
func NewSchedulerStore(
	appLogin string,
	commits StateCommitPort,
	limits usecase.SchedulerLimits,
) (*SchedulerStore, error) {
	if !appBotLoginPattern.MatchString(appLogin) ||
		commits == nil ||
		limits.MaxActive <= 0 ||
		limits.MaxPerPullRequest != 1 ||
		limits.MaxFirstTimeExternal <= 0 ||
		limits.MaxFirstTimeExternal > limits.MaxActive {
		return nil, errors.New("Review Scheduler Store configuration is invalid")
	}
	return &SchedulerStore{
		appLogin: appLogin,
		commits:  commits,
		limits:   limits,
	}, nil
}

// Load verifies the current App-signed rolling checkpoint and its immediate
// predecessor. Older commits remain append-only audit history but are not
// replayed on the repository-wide scheduling hot path.
func (store *SchedulerStore) Load(
	ctx context.Context,
) (LoadedSchedulerState, bool, error) {
	if store == nil || ctx == nil {
		return LoadedSchedulerState{}, false, errors.New(
			"Review Scheduler Store load request is invalid",
		)
	}
	head, found, err := store.commits.StateRefHead(ctx, schedulerStateBranch)
	if err != nil || !found {
		return LoadedSchedulerState{}, found, err
	}
	if !gitSHAPattern.MatchString(head) {
		return LoadedSchedulerState{}, false, errors.New(
			"Review scheduler head is invalid",
		)
	}

	latestRecord, latest, latestLegacy, err := store.readCheckpoint(ctx, head)
	if err != nil {
		return LoadedSchedulerState{}, false, err
	}
	if latest.Sequence == 1 {
		if latestLegacy || latestRecord.ParentSHA != latest.SourceSHA {
			return LoadedSchedulerState{}, false, errors.New(
				"initial Review scheduler has the wrong source parent",
			)
		}
		return LoadedSchedulerState{HeadSHA: head, State: latest}, true, nil
	}
	if !gitSHAPattern.MatchString(latestRecord.ParentSHA) {
		return LoadedSchedulerState{}, false, errors.New(
			"Review scheduler predecessor is invalid",
		)
	}
	_, previous, previousLegacy, err := store.readCheckpoint(
		ctx,
		latestRecord.ParentSHA,
	)
	if err != nil {
		return LoadedSchedulerState{}, false, err
	}
	previousDigest, err := usecase.SchedulerStateDigest(
		previous,
		store.limits,
	)
	if err != nil {
		return LoadedSchedulerState{}, false, errors.New(
			"Review scheduler rolling checkpoint is not contiguous",
		)
	}
	if !latestLegacy &&
		latest.PreviousStateDigest == previousDigest &&
		previous.Sequence+1 == latest.Sequence &&
		!previous.UpdatedAt.After(latest.UpdatedAt) &&
		previous.SourceSHA == latest.SourceSHA {
		return LoadedSchedulerState{HeadSHA: head, State: latest}, true, nil
	}
	if latestLegacy && !previousLegacy &&
		latest.Sequence == previous.Sequence {
		latestDigest, digestErr := usecase.SchedulerStateDigest(
			latest,
			store.limits,
		)
		if digestErr != nil || latestDigest != previousDigest {
			return LoadedSchedulerState{}, false, errors.New(
				"Review scheduler rolling checkpoint is not contiguous",
			)
		}
		normalized, normalizeErr := normalizeLoadedScheduler(
			latest,
			store.limits,
		)
		if normalizeErr != nil {
			return LoadedSchedulerState{}, false, errors.New(
				"Review scheduler rolling checkpoint is not contiguous",
			)
		}
		return LoadedSchedulerState{
			HeadSHA: head,
			State:   normalized,
		}, true, nil
	}
	return LoadedSchedulerState{}, false, errors.New(
		"Review scheduler rolling checkpoint is not contiguous",
	)
}

func (store *SchedulerStore) readCheckpoint(
	ctx context.Context,
	commitSHA string,
) (StateCommitRecord, usecase.SchedulerState, bool, error) {
	record, err := store.commits.ReadStateCommit(
		ctx,
		commitSHA,
		schedulerStatePath,
	)
	if err != nil {
		return StateCommitRecord{}, usecase.SchedulerState{}, false, err
	}
	if err := validateStateRecordTrust(
		record,
		commitSHA,
		schedulerStatePath,
		store.appLogin,
	); err != nil {
		return StateCommitRecord{}, usecase.SchedulerState{}, false, err
	}
	state, err := usecase.DecodeSchedulerState(
		bytes.NewReader(record.Content),
		512<<10,
		store.limits,
	)
	if err != nil ||
		record.Message != fmt.Sprintf(
			"review(scheduler): sequence %d",
			state.Sequence,
		) {
		return StateCommitRecord{}, usecase.SchedulerState{}, false, errors.New(
			"Review scheduler commit content is invalid",
		)
	}
	canonical, err := usecase.CanonicalSchedulerState(state, store.limits)
	if err != nil {
		return StateCommitRecord{}, usecase.SchedulerState{}, false, errors.New(
			"Review scheduler commit is not canonical",
		)
	}
	if bytes.Equal(canonical, record.Content) {
		return record, state, false, nil
	}
	legacy, legacyErr := json.Marshal(state)
	if legacyErr != nil || !bytes.Equal(legacy, record.Content) {
		return StateCommitRecord{}, usecase.SchedulerState{}, false, errors.New(
			"Review scheduler commit is not canonical",
		)
	}
	return record, state, true, nil
}

func normalizeLoadedScheduler(
	state usecase.SchedulerState,
	limits usecase.SchedulerLimits,
) (usecase.SchedulerState, error) {
	body, err := usecase.CanonicalSchedulerState(state, limits)
	if err != nil {
		return usecase.SchedulerState{}, err
	}
	return usecase.DecodeSchedulerState(
		bytes.NewReader(body),
		512<<10,
		limits,
	)
}

// Advance appends one canonical scheduler state at the exact expected head.
func (store *SchedulerStore) Advance(
	ctx context.Context,
	state usecase.SchedulerState,
	expectedParentSHA string,
	existingBranch bool,
) (string, error) {
	if store == nil || ctx == nil ||
		!gitSHAPattern.MatchString(expectedParentSHA) ||
		(!existingBranch &&
			(state.Sequence != 1 ||
				expectedParentSHA != state.SourceSHA)) ||
		(existingBranch && state.Sequence == 1) {
		return "", errors.New(
			"Review Scheduler Store advance request is invalid",
		)
	}
	content, err := usecase.CanonicalSchedulerState(state, store.limits)
	if err != nil {
		return "", err
	}
	result, err := store.commits.PublishStateCommit(ctx, StateCommitRequest{
		Branch: schedulerStateBranch, Path: schedulerStatePath,
		ExpectedParentSHA: expectedParentSHA,
		ExistingBranch:    existingBranch,
		Message: fmt.Sprintf(
			"review(scheduler): sequence %d",
			state.Sequence,
		),
		Content: content,
	})
	if err != nil {
		return "", err
	}
	if err := validatePublishedState(
		result,
		expectedParentSHA,
		schedulerStatePath,
		content,
		store.appLogin,
	); err != nil {
		return "", err
	}
	return result.CommitSHA, nil
}
