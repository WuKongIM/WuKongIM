package meta

import (
	"context"
	"errors"
)

func (s *Store) UpsertOnboardingJob(ctx context.Context, job NodeOnboardingJob) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}
	var err error
	job, err = normalizeAndValidateOnboardingJob(job, ErrInvalidArgument)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.writeValueLocked(encodeOnboardingJobKey(job.JobID), encodeOnboardingJob(job))
}

func (s *Store) UpsertOnboardingJobAssignmentTask(ctx context.Context, job NodeOnboardingJob, assignment SlotAssignment, task ReconcileTask) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}
	job, assignment, task, err := validateOnboardingJobAssignmentTask(job, assignment, task)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.writeBatchLocked([]batchWrite{
		{key: encodeOnboardingJobKey(job.JobID), value: encodeOnboardingJob(job)},
		{key: encodeGroupKey(recordPrefixAssignment, assignment.SlotID), value: encodeGroupAssignment(assignment)},
		{key: encodeGroupKey(recordPrefixTask, task.SlotID), value: encodeReconcileTask(task)},
	})
}

func (s *Store) GuardedUpsertOnboardingJob(ctx context.Context, job NodeOnboardingJob, expectedStatus *OnboardingJobStatus, assignment *SlotAssignment, task *ReconcileTask) (bool, error) {
	if err := s.ensureOpen(); err != nil {
		return false, err
	}
	if err := s.checkContext(ctx); err != nil {
		return false, err
	}
	var err error
	job, err = normalizeAndValidateOnboardingJob(job, ErrInvalidArgument)
	if err != nil {
		return false, err
	}
	writes := []batchWrite{{key: encodeOnboardingJobKey(job.JobID), value: encodeOnboardingJob(job)}}
	if assignment != nil {
		normalized, err := normalizeAndValidateAssignmentForOnboarding(*assignment)
		if err != nil {
			return false, err
		}
		assignment = &normalized
		writes = append(writes, batchWrite{key: encodeGroupKey(recordPrefixAssignment, assignment.SlotID), value: encodeGroupAssignment(*assignment)})
	}
	if task != nil {
		normalized, err := normalizeAndValidateTaskForOnboarding(*task)
		if err != nil {
			return false, err
		}
		task = &normalized
		writes = append(writes, batchWrite{key: encodeGroupKey(recordPrefixTask, task.SlotID), value: encodeReconcileTask(*task)})
	}
	if assignment != nil && task != nil && assignment.SlotID != task.SlotID {
		return false, ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if expectedStatus != nil {
		stored, err := s.getOnboardingJobLocked(job.JobID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		if stored.Status != *expectedStatus {
			return false, nil
		}
	}
	if job.Status == OnboardingJobStatusRunning {
		running, err := s.listRunningOnboardingJobsLocked(ctx)
		if err != nil {
			return false, err
		}
		for _, runningJob := range running {
			if runningJob.JobID != job.JobID {
				return false, nil
			}
		}
	}

	return true, s.writeBatchLocked(writes)
}

func (s *Store) GetOnboardingJob(ctx context.Context, jobID string) (NodeOnboardingJob, error) {
	if err := s.ensureOpen(); err != nil {
		return NodeOnboardingJob{}, err
	}
	if jobID == "" {
		return NodeOnboardingJob{}, ErrInvalidArgument
	}
	if err := s.checkContext(ctx); err != nil {
		return NodeOnboardingJob{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.getOnboardingJobLocked(jobID)
}

func (s *Store) ListOnboardingJobs(ctx context.Context, limit int, cursor string) ([]NodeOnboardingJob, string, bool, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, "", false, err
	}
	if err := s.checkContext(ctx); err != nil {
		return nil, "", false, err
	}
	if limit < 0 {
		return nil, "", false, ErrInvalidArgument
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	jobs, err := s.listOnboardingJobsLocked(ctx)
	if err != nil {
		return nil, "", false, err
	}
	start := 0
	if cursor != "" {
		for start < len(jobs) && jobs[start].JobID <= cursor {
			start++
		}
	}
	if start >= len(jobs) {
		return nil, "", false, nil
	}
	end := len(jobs)
	hasMore := false
	if limit > 0 && start+limit < len(jobs) {
		end = start + limit
		hasMore = true
	}
	out := append([]NodeOnboardingJob(nil), jobs[start:end]...)
	nextCursor := ""
	if hasMore && len(out) > 0 {
		nextCursor = out[len(out)-1].JobID
	}
	return out, nextCursor, hasMore, nil
}

func (s *Store) ListRunningOnboardingJobs(ctx context.Context) ([]NodeOnboardingJob, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := s.checkContext(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.listRunningOnboardingJobsLocked(ctx)
}

func (s *Store) getOnboardingJobLocked(jobID string) (NodeOnboardingJob, error) {
	key := encodeOnboardingJobKey(jobID)
	value, err := s.getValueLocked(key)
	if err != nil {
		return NodeOnboardingJob{}, err
	}
	return decodeOnboardingJob(key, value)
}

func (s *Store) listOnboardingJobsLocked(ctx context.Context) ([]NodeOnboardingJob, error) {
	jobs, err := listRecords(ctx, s.db, recordPrefixOnboardingJob, decodeOnboardingJob)
	if err != nil {
		return nil, err
	}
	sortOnboardingJobsByID(jobs)
	return jobs, nil
}

func (s *Store) listRunningOnboardingJobsLocked(ctx context.Context) ([]NodeOnboardingJob, error) {
	jobs, err := s.listOnboardingJobsLocked(ctx)
	if err != nil {
		return nil, err
	}
	running := make([]NodeOnboardingJob, 0, 1)
	for _, job := range jobs {
		if job.Status == OnboardingJobStatusRunning {
			running = append(running, job)
		}
	}
	return running, nil
}

func validateOnboardingJobAssignmentTask(job NodeOnboardingJob, assignment SlotAssignment, task ReconcileTask) (NodeOnboardingJob, SlotAssignment, ReconcileTask, error) {
	var err error
	job, err = normalizeAndValidateOnboardingJob(job, ErrInvalidArgument)
	if err != nil {
		return NodeOnboardingJob{}, SlotAssignment{}, ReconcileTask{}, err
	}
	assignment, err = normalizeAndValidateAssignmentForOnboarding(assignment)
	if err != nil {
		return NodeOnboardingJob{}, SlotAssignment{}, ReconcileTask{}, err
	}
	task, err = normalizeAndValidateTaskForOnboarding(task)
	if err != nil {
		return NodeOnboardingJob{}, SlotAssignment{}, ReconcileTask{}, err
	}
	if assignment.SlotID != task.SlotID {
		return NodeOnboardingJob{}, SlotAssignment{}, ReconcileTask{}, ErrInvalidArgument
	}
	return job, assignment, task, nil
}

func normalizeAndValidateAssignmentForOnboarding(assignment SlotAssignment) (SlotAssignment, error) {
	if assignment.SlotID == 0 {
		return SlotAssignment{}, ErrInvalidArgument
	}
	assignment = normalizeGroupAssignment(assignment)
	if err := validateRequiredPeerSet(assignment.DesiredPeers, ErrInvalidArgument); err != nil {
		return SlotAssignment{}, err
	}
	return assignment, nil
}

func normalizeAndValidateTaskForOnboarding(task ReconcileTask) (ReconcileTask, error) {
	task = normalizeReconcileTask(task)
	if task.SlotID == 0 || !validTaskKind(task.Kind) || !validTaskStep(task.Step) || !validTaskStatus(task.Status) {
		return ReconcileTask{}, ErrInvalidArgument
	}
	if err := validateReconcileTaskState(task, ErrInvalidArgument); err != nil {
		return ReconcileTask{}, err
	}
	return task, nil
}
