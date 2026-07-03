package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/taskaudit"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	controllerTaskAuditQueueSize      = 1024
	controllerTaskAuditFileName       = "controller-tasks.jsonl"
	controllerTaskAuditLegacyFileName = "controller-" + "v2-tasks.jsonl"
)

// controllerTaskAuditRuntime adapts Controller task transitions to a bounded JSONL audit store.
type controllerTaskAuditRuntime struct {
	path    string
	opts    taskaudit.Options
	logger  wklog.Logger
	metrics *obsmetrics.Registry

	mu     sync.Mutex
	store  *taskaudit.Store
	queue  chan []taskaudit.Event
	done   chan struct{}
	closed bool
}

type controllerTaskAuditControlSnapshotReader interface {
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
}

func newControllerTaskAuditRuntime(path string, logger wklog.Logger) *controllerTaskAuditRuntime {
	if logger == nil {
		logger = wklog.NewNop()
	}
	return &controllerTaskAuditRuntime{
		path:   strings.TrimSpace(path),
		opts:   taskaudit.Options{},
		logger: logger.Named("controller_task_audit"),
	}
}

func (a *App) wireControllerTaskAudit(clusterCfg *cluster.Config) {
	if a == nil || clusterCfg == nil {
		return
	}
	path := controllerTaskAuditPath(clusterCfg.DataDir)
	if path == "" {
		return
	}
	if a.controllerTaskAudit == nil {
		a.controllerTaskAudit = newControllerTaskAuditRuntime(path, a.logger)
	}
	if a.metrics != nil {
		a.controllerTaskAudit.metrics = a.metrics
	}
	clusterCfg.Control.TaskTransitionObserver = combineControllerTaskTransitionObservers(clusterCfg.Control.TaskTransitionObserver, a.controllerTaskAudit)
}

func controllerTaskAuditPath(dataDir string) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return ""
	}
	dir := filepath.Join(dataDir, "observability", "task-audit")
	current := filepath.Join(dir, controllerTaskAuditFileName)
	legacy := filepath.Join(dir, controllerTaskAuditLegacyFileName)
	if fileExists(current) {
		return current
	}
	if fileExists(legacy) {
		return legacy
	}
	return current
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ObserveControllerTaskTransitions records Controller task transitions without blocking Raft apply on file IO.
func (r *controllerTaskAuditRuntime) ObserveControllerTaskTransitions(transitions []controller.TaskTransition) {
	events := controllerTaskAuditEventsForTransitions(transitions)
	if len(events) == 0 || r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.startWorkerLocked()
	select {
	case r.queue <- events:
	default:
		r.logger.Warn("controller task audit queue full; dropping task transition events",
			wklog.Event("internal.app.controller_task_audit_queue_full"),
			wklog.Int("events", len(events)),
		)
	}
}

// AppendSnapshotTasks records a best-effort startup snapshot for currently active Controller tasks.
func (r *controllerTaskAuditRuntime) AppendSnapshotTasks(ctx context.Context, snapshot control.Snapshot) error {
	if r == nil || len(snapshot.Tasks) == 0 {
		return nil
	}
	events := make([]taskaudit.Event, 0, len(snapshot.Tasks))
	for _, task := range snapshot.Tasks {
		events = append(events, controllerTaskAuditSnapshotEvent(snapshot.Revision, task))
	}
	return r.appendEvents(ctx, events)
}

func (r *controllerTaskAuditRuntime) ListControllerTaskAudits(ctx context.Context, req managementusecase.ControllerTaskAuditListRequest) (managementusecase.ControllerTaskAuditListResponse, error) {
	store, err := r.ensureStore(ctx)
	if err != nil {
		return managementusecase.ControllerTaskAuditListResponse{}, controllerTaskAuditManagementError(err)
	}
	resp, err := store.List(ctx, taskaudit.ListRequest{
		Kind:    req.Kind,
		Status:  req.Status,
		Keyword: req.Keyword,
		SlotID:  req.SlotID,
		NodeID:  req.NodeID,
		Limit:   req.Limit,
	})
	if err != nil {
		return managementusecase.ControllerTaskAuditListResponse{}, controllerTaskAuditManagementError(err)
	}
	r.observeTaskOldestAge(ctx, store)
	return controllerTaskAuditListResponse(resp), nil
}

func (r *controllerTaskAuditRuntime) ControllerTaskAuditEvents(ctx context.Context, taskID string) (managementusecase.ControllerTaskAuditEventsResponse, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return managementusecase.ControllerTaskAuditEventsResponse{}, metadb.ErrInvalidArgument
	}
	store, err := r.ensureStore(ctx)
	if err != nil {
		return managementusecase.ControllerTaskAuditEventsResponse{}, controllerTaskAuditManagementError(err)
	}
	resp, err := store.Events(ctx, taskID)
	if err != nil {
		return managementusecase.ControllerTaskAuditEventsResponse{}, controllerTaskAuditManagementError(err)
	}
	return controllerTaskAuditEventsResponse(resp), nil
}

func (r *controllerTaskAuditRuntime) Close() error {
	if r == nil {
		return nil
	}
	var done <-chan struct{}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		if r.queue != nil {
			close(r.queue)
			done = r.done
		}
	}
	r.mu.Unlock()
	if done != nil {
		<-done
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.store == nil {
		return nil
	}
	err := r.store.Close()
	r.store = nil
	return err
}

func (r *controllerTaskAuditRuntime) startWorkerLocked() {
	if r.queue != nil {
		return
	}
	r.queue = make(chan []taskaudit.Event, controllerTaskAuditQueueSize)
	r.done = make(chan struct{})
	go r.run()
}

func (r *controllerTaskAuditRuntime) run() {
	defer close(r.done)
	for events := range r.queue {
		if err := r.appendEvents(context.Background(), events); err != nil {
			r.logger.Warn("controller task audit append failed",
				wklog.Event("internal.app.controller_task_audit_append_failed"),
				wklog.Error(err),
			)
		}
	}
}

func (r *controllerTaskAuditRuntime) appendEvents(ctx context.Context, events []taskaudit.Event) error {
	if len(events) == 0 {
		return nil
	}
	store, err := r.ensureStore(ctx)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := store.Append(ctx, event); err != nil {
			return err
		}
	}
	r.observeTaskOldestAge(ctx, store)
	return nil
}

func (r *controllerTaskAuditRuntime) ensureStore(ctx context.Context) (*taskaudit.Store, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if r == nil || strings.TrimSpace(r.path) == "" {
		return nil, taskaudit.ErrUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, taskaudit.ErrUnavailable
	}
	if r.store != nil {
		return r.store, nil
	}
	store, err := taskaudit.Open(r.path, r.opts)
	if err != nil {
		return nil, err
	}
	r.store = store
	return store, nil
}

func (a *App) backfillControllerTaskAudit(ctx context.Context) error {
	if a == nil || a.controllerTaskAudit == nil {
		return nil
	}
	reader, ok := a.cluster.(controllerTaskAuditControlSnapshotReader)
	if !ok {
		return nil
	}
	snapshot, err := reader.LocalControlSnapshot(ctx)
	if err != nil {
		return err
	}
	return a.controllerTaskAudit.AppendSnapshotTasks(ctx, snapshot)
}

func (a *App) closeControllerTaskAudit() error {
	if a == nil || a.controllerTaskAudit == nil {
		return nil
	}
	return a.controllerTaskAudit.Close()
}

func controllerTaskAuditEventsForTransitions(transitions []controller.TaskTransition) []taskaudit.Event {
	events := make([]taskaudit.Event, 0, len(transitions))
	for _, transition := range transitions {
		if event, ok := controllerTaskAuditEventForTransition(transition); ok {
			events = append(events, event)
		}
	}
	return events
}

func controllerTaskAuditEventForTransition(transition controller.TaskTransition) (taskaudit.Event, bool) {
	task, ok := controllerTaskAuditTaskForTransition(transition)
	if !ok || task.TaskID == "" {
		return taskaudit.Event{}, false
	}
	eventType := taskaudit.EventRunning
	switch {
	case transition.AfterValid && !transition.BeforeValid:
		eventType = taskaudit.EventCreated
	case transition.BeforeValid && !transition.AfterValid:
		eventType = taskaudit.EventCompleted
	case task.Status == controller.TaskStatusFailed:
		eventType = taskaudit.EventFailed
	case transition.ParticipantNode != 0 || transition.CommandKind == controller.CommandKindReportTaskProgress:
		eventType = taskaudit.EventParticipantProgress
	}
	event := controllerTaskAuditEventBase(task, eventType)
	event.AppliedRaftIndex = transition.AppliedRaftIndex
	event.AppliedRaftTerm = transition.AppliedRaftTerm
	event.CommandKind = string(transition.CommandKind)
	event.ParticipantNode = transition.ParticipantNode
	event.OccurredAt = transition.IssuedAt
	event.Summary = controllerTaskAuditSummary(eventType, task, transition.ParticipantNode)
	event.Reason = controllerTaskAuditReason(eventType, task, transition.ParticipantNode)
	return event, true
}

func controllerTaskAuditTaskForTransition(transition controller.TaskTransition) (controller.ReconcileTask, bool) {
	if transition.AfterValid {
		return transition.After, true
	}
	if transition.BeforeValid {
		return transition.Before, true
	}
	return controller.ReconcileTask{}, false
}

func controllerTaskAuditSnapshotEvent(revision uint64, task control.ReconcileTask) taskaudit.Event {
	controllerTask := controllerTaskFromControlTask(task)
	event := controllerTaskAuditEventBase(controllerTask, taskaudit.EventSnapshot)
	event.EventID = fmt.Sprintf("%s:%d:snapshot", task.TaskID, revision)
	event.AppliedRaftIndex = revision
	event.Summary = controllerTaskAuditSummary(taskaudit.EventSnapshot, controllerTask, 0)
	return event
}

func controllerTaskAuditEventBase(task controller.ReconcileTask, eventType taskaudit.EventType) taskaudit.Event {
	status := string(task.Status)
	if eventType == taskaudit.EventCompleted {
		status = "completed"
	}
	return taskaudit.Event{
		TaskID:     task.TaskID,
		Type:       eventType,
		Kind:       string(task.Kind),
		Status:     status,
		SlotID:     task.SlotID,
		LeaderID:   controllerTaskAuditLeaderID(task),
		SourceNode: task.SourceNode,
		TargetNode: task.TargetNode,
		Details:    controllerTaskAuditDetails(task),
	}
}

func controllerTaskAuditLeaderID(task controller.ReconcileTask) uint64 {
	switch task.Kind {
	case controller.TaskKindBootstrap, controller.TaskKindLeaderTransfer:
		return task.TargetNode
	default:
		return 0
	}
}

func controllerTaskAuditDetails(task controller.ReconcileTask) map[string]any {
	details := map[string]any{
		"step":              string(task.Step),
		"completion_policy": string(task.CompletionPolicy),
		"config_epoch":      task.ConfigEpoch,
		"attempt":           task.Attempt,
		"phase_index":       task.PhaseIndex,
	}
	if len(task.TargetPeers) > 0 {
		details["target_peers"] = append([]uint64(nil), task.TargetPeers...)
	}
	if len(task.ParticipantProgress) > 0 {
		progress := make([]map[string]any, 0, len(task.ParticipantProgress))
		for _, item := range task.ParticipantProgress {
			progress = append(progress, map[string]any{
				"node_id":    item.NodeID,
				"attempt":    item.Attempt,
				"status":     string(item.Status),
				"last_error": item.LastError,
			})
		}
		details["participant_progress"] = progress
	}
	if task.ObservedConfigIndex != 0 {
		details["observed_config_index"] = task.ObservedConfigIndex
	}
	if len(task.ObservedVoters) > 0 {
		details["observed_voters"] = append([]uint64(nil), task.ObservedVoters...)
	}
	if len(task.ObservedLearners) > 0 {
		details["observed_learners"] = append([]uint64(nil), task.ObservedLearners...)
	}
	return details
}

func controllerTaskAuditSummary(eventType taskaudit.EventType, task controller.ReconcileTask, participantNode uint64) string {
	switch eventType {
	case taskaudit.EventCreated:
		return fmt.Sprintf("created %s task for slot %d", task.Kind, task.SlotID)
	case taskaudit.EventParticipantProgress:
		return fmt.Sprintf("participant %d reported %s for %s task on slot %d", participantNode, controllerTaskAuditParticipantStatus(task, participantNode), task.Kind, task.SlotID)
	case taskaudit.EventFailed:
		return fmt.Sprintf("failed %s task for slot %d", task.Kind, task.SlotID)
	case taskaudit.EventCompleted:
		return fmt.Sprintf("completed %s task for slot %d", task.Kind, task.SlotID)
	case taskaudit.EventSnapshot:
		return fmt.Sprintf("snapshot active %s task for slot %d", task.Kind, task.SlotID)
	default:
		return fmt.Sprintf("%s task advanced to %s for slot %d", task.Kind, task.Step, task.SlotID)
	}
}

func controllerTaskAuditReason(eventType taskaudit.EventType, task controller.ReconcileTask, participantNode uint64) string {
	if progress, ok := controllerTaskAuditParticipant(task, participantNode); ok && progress.LastError != "" {
		return progress.LastError
	}
	if task.Status == controller.TaskStatusFailed || eventType == taskaudit.EventFailed {
		return task.LastError
	}
	return ""
}

func controllerTaskAuditParticipantStatus(task controller.ReconcileTask, participantNode uint64) string {
	if progress, ok := controllerTaskAuditParticipant(task, participantNode); ok {
		return string(progress.Status)
	}
	if participantNode == 0 {
		return "progress"
	}
	return "unknown"
}

func controllerTaskAuditParticipant(task controller.ReconcileTask, participantNode uint64) (controller.TaskParticipantProgress, bool) {
	if participantNode == 0 {
		return controller.TaskParticipantProgress{}, false
	}
	for _, progress := range task.ParticipantProgress {
		if progress.NodeID == participantNode {
			return progress, true
		}
	}
	return controller.TaskParticipantProgress{}, false
}

func controllerTaskFromControlTask(task control.ReconcileTask) controller.ReconcileTask {
	return controller.ReconcileTask{
		TaskID:              task.TaskID,
		SlotID:              task.SlotID,
		Kind:                task.Kind,
		Step:                task.Step,
		SourceNode:          task.SourceNode,
		TargetNode:          task.TargetNode,
		TargetPeers:         append([]uint64(nil), task.TargetPeers...),
		CompletionPolicy:    task.CompletionPolicy,
		ParticipantProgress: append([]controller.TaskParticipantProgress(nil), task.ParticipantProgress...),
		ConfigEpoch:         task.ConfigEpoch,
		Attempt:             task.Attempt,
		Status:              task.Status,
		LastError:           task.LastError,
		PhaseIndex:          task.PhaseIndex,
		ObservedConfigIndex: task.ObservedConfigIndex,
		ObservedVoters:      append([]uint64(nil), task.ObservedVoters...),
		ObservedLearners:    append([]uint64(nil), task.ObservedLearners...),
	}
}

func controllerTaskAuditManagementError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, taskaudit.ErrUnavailable):
		return managementusecase.ErrControllerTaskAuditUnavailable
	case errors.Is(err, taskaudit.ErrTaskNotFound):
		return managementusecase.ErrControllerTaskAuditNotFound
	default:
		return err
	}
}

func controllerTaskAuditListResponse(resp taskaudit.ListResponse) managementusecase.ControllerTaskAuditListResponse {
	items := make([]managementusecase.ControllerTaskAuditSnapshot, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, controllerTaskAuditSnapshot(item))
	}
	return managementusecase.ControllerTaskAuditListResponse{
		Total:     resp.Total,
		Limit:     resp.Limit,
		Truncated: resp.Truncated,
		Items:     items,
	}
}

func controllerTaskAuditEventsResponse(resp taskaudit.EventsResponse) managementusecase.ControllerTaskAuditEventsResponse {
	events := make([]managementusecase.ControllerTaskAuditEvent, 0, len(resp.Events))
	for _, event := range resp.Events {
		events = append(events, controllerTaskAuditEvent(event))
	}
	return managementusecase.ControllerTaskAuditEventsResponse{
		Task:      controllerTaskAuditSnapshot(resp.Task),
		Events:    events,
		Truncated: resp.Truncated,
	}
}

func (r *controllerTaskAuditRuntime) observeTaskOldestAge(ctx context.Context, store *taskaudit.Store) {
	if r == nil || r.metrics == nil || store == nil {
		return
	}
	resp, err := store.List(ctx, taskaudit.ListRequest{Limit: controllerTaskAuditMetricListLimit(r)})
	if err != nil {
		return
	}
	r.metrics.Controller.SetTaskOldestAge(controllerTaskAuditOldestAges(resp.Items, controllerTaskAuditNow(r)))
}

func controllerTaskAuditMetricListLimit(r *controllerTaskAuditRuntime) int {
	if r != nil && r.opts.MaxTasks > 0 {
		return r.opts.MaxTasks
	}
	return taskaudit.DefaultMaxTasks
}

func controllerTaskAuditNow(r *controllerTaskAuditRuntime) time.Time {
	if r != nil && r.opts.Now != nil {
		return r.opts.Now().UTC()
	}
	return time.Now().UTC()
}

func controllerTaskAuditOldestAges(snapshots []taskaudit.Snapshot, now time.Time) map[obsmetrics.ControllerTaskAgeKey]float64 {
	ages := make(map[obsmetrics.ControllerTaskAgeKey]float64)
	for _, snapshot := range snapshots {
		if snapshot.StartedAt.IsZero() {
			continue
		}
		age := now.Sub(snapshot.StartedAt).Seconds()
		if age < 0 {
			age = 0
		}
		key := obsmetrics.ControllerTaskAgeKey{
			Kind:   snapshot.Kind,
			Status: snapshot.Status,
			Step:   snapshot.Step,
			Source: "audit",
		}
		if existing, ok := ages[key]; !ok || age > existing {
			ages[key] = age
		}
	}
	return ages
}

func controllerTaskAuditSnapshot(snapshot taskaudit.Snapshot) managementusecase.ControllerTaskAuditSnapshot {
	return managementusecase.ControllerTaskAuditSnapshot{
		TaskID:                snapshot.TaskID,
		Kind:                  snapshot.Kind,
		Status:                snapshot.Status,
		Step:                  snapshot.Step,
		SlotID:                snapshot.SlotID,
		LeaderID:              snapshot.LeaderID,
		SourceNode:            snapshot.SourceNode,
		TargetNode:            snapshot.TargetNode,
		FirstAppliedRaftIndex: snapshot.FirstAppliedRaftIndex,
		LastAppliedRaftIndex:  snapshot.LastAppliedRaftIndex,
		StartedAt:             snapshot.StartedAt,
		CompletedAt:           snapshot.CompletedAt,
		EventCount:            snapshot.EventCount,
		Truncated:             snapshot.Truncated,
		Summary:               snapshot.Summary,
		LastReason:            snapshot.LastReason,
	}
}

func controllerTaskAuditEvent(event taskaudit.Event) managementusecase.ControllerTaskAuditEvent {
	return managementusecase.ControllerTaskAuditEvent{
		EventID:          event.EventID,
		TaskID:           event.TaskID,
		Type:             string(event.Type),
		Kind:             event.Kind,
		Status:           event.Status,
		SlotID:           event.SlotID,
		LeaderID:         event.LeaderID,
		SourceNode:       event.SourceNode,
		TargetNode:       event.TargetNode,
		AppliedRaftIndex: event.AppliedRaftIndex,
		AppliedRaftTerm:  event.AppliedRaftTerm,
		CommandKind:      event.CommandKind,
		ParticipantNode:  event.ParticipantNode,
		OccurredAt:       event.OccurredAt,
		Summary:          event.Summary,
		Reason:           event.Reason,
		Details:          event.Details,
	}
}

type controllerTaskTransitionObservers []controller.TaskTransitionObserver

func combineControllerTaskTransitionObservers(first controller.TaskTransitionObserver, rest ...controller.TaskTransitionObserver) controller.TaskTransitionObserver {
	observers := make(controllerTaskTransitionObservers, 0, 1+len(rest))
	if first != nil {
		observers = append(observers, first)
	}
	for _, observer := range rest {
		if observer != nil {
			observers = append(observers, observer)
		}
	}
	switch len(observers) {
	case 0:
		return nil
	case 1:
		return observers[0]
	default:
		return observers
	}
}

func (o controllerTaskTransitionObservers) ObserveControllerTaskTransitions(transitions []controller.TaskTransition) {
	for _, observer := range o {
		observer.ObserveControllerTaskTransitions(transitions)
	}
}
