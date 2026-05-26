package management

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	distributedTaskDefaultLimit     = 50
	distributedTaskMaxLimit         = 200
	distributedTaskSourceScanLimit  = 512
	distributedTaskChannelIDVersion = 1
)

var (
	// ErrDistributedTasksUnavailable reports that every task source failed.
	ErrDistributedTasksUnavailable = errors.New("management: distributed task sources unavailable")
	// ErrDistributedTaskNotFound reports that one normalized task detail was not found.
	ErrDistributedTaskNotFound = errors.New("management: distributed task not found")
)

// DistributedTaskDomain identifies a source family in the read-only task center.
type DistributedTaskDomain string

const (
	// DistributedTaskDomainSlotReconcile describes controller Slot reconcile tasks.
	DistributedTaskDomainSlotReconcile DistributedTaskDomain = "slot_reconcile"
	// DistributedTaskDomainNodeOnboarding describes durable node onboarding jobs.
	DistributedTaskDomainNodeOnboarding DistributedTaskDomain = "node_onboarding"
	// DistributedTaskDomainNodeScaleIn describes manager-derived node scale-in state.
	DistributedTaskDomainNodeScaleIn DistributedTaskDomain = "node_scale_in"
	// DistributedTaskDomainChannelMigration describes channel leader and replica migration tasks.
	DistributedTaskDomainChannelMigration DistributedTaskDomain = "channel_migration"
)

// DistributedTaskStatus is the normalized manager-facing task lifecycle state.
type DistributedTaskStatus string

const (
	// DistributedTaskStatusPending means the task is waiting to run.
	DistributedTaskStatusPending DistributedTaskStatus = "pending"
	// DistributedTaskStatusRunning means the task is actively making progress.
	DistributedTaskStatusRunning DistributedTaskStatus = "running"
	// DistributedTaskStatusRetrying means the task is waiting for a retry time.
	DistributedTaskStatusRetrying DistributedTaskStatus = "retrying"
	// DistributedTaskStatusBlocked means safety checks or dependencies block progress.
	DistributedTaskStatusBlocked DistributedTaskStatus = "blocked"
	// DistributedTaskStatusFailed means the task needs operator attention.
	DistributedTaskStatusFailed DistributedTaskStatus = "failed"
	// DistributedTaskStatusCompleted means the task reached a successful terminal state.
	DistributedTaskStatusCompleted DistributedTaskStatus = "completed"
	// DistributedTaskStatusCancelled means the task was explicitly cancelled or aborted.
	DistributedTaskStatusCancelled DistributedTaskStatus = "cancelled"
	// DistributedTaskStatusUnknown means the source returned an unrecognized state.
	DistributedTaskStatusUnknown DistributedTaskStatus = "unknown"
)

// DistributedTaskScopeType describes the primary object affected by a task.
type DistributedTaskScopeType string

const (
	// DistributedTaskScopeSlot identifies a physical Slot scope.
	DistributedTaskScopeSlot DistributedTaskScopeType = "slot"
	// DistributedTaskScopeNode identifies a cluster node scope.
	DistributedTaskScopeNode DistributedTaskScopeType = "node"
	// DistributedTaskScopeChannel identifies a channel scope.
	DistributedTaskScopeChannel DistributedTaskScopeType = "channel"
	// DistributedTaskScopeJob identifies an operator-reviewed durable job scope.
	DistributedTaskScopeJob DistributedTaskScopeType = "job"
)

// DistributedTaskQuery contains filters for the read-only distributed task list.
type DistributedTaskQuery struct {
	// Domain filters tasks by source family.
	Domain DistributedTaskDomain
	// Status filters tasks by normalized lifecycle state.
	Status DistributedTaskStatus
	// NodeID filters tasks that mention the node as source, target, owner, or scope.
	NodeID uint64
	// Scope filters tasks by primary affected object type.
	Scope DistributedTaskScopeType
	// Keyword filters tasks by stable text fields.
	Keyword string
	// Limit caps the returned page size.
	Limit int
	// Offset skips a deterministic number of matching tasks.
	Offset int
}

// DistributedTaskScope describes the primary object affected by a task.
type DistributedTaskScope struct {
	// Type is the scope object type.
	Type DistributedTaskScopeType
	// ID is the stable human-readable scope identity.
	ID string
	// SlotID is set when Type is slot.
	SlotID uint32
	// ChannelID is set when Type is channel.
	ChannelID string
	// ChannelType is set when Type is channel.
	ChannelType int64
	// NodeID is set when Type is node.
	NodeID uint64
}

// DistributedTask is the normalized read-only task row used by manager APIs.
type DistributedTask struct {
	// ID is stable within the task domain and can be used for detail lookup.
	ID string
	// Domain is the task source family.
	Domain DistributedTaskDomain
	// Kind is the source-specific task kind.
	Kind string
	// Status is the normalized lifecycle state.
	Status DistributedTaskStatus
	// Phase is the source-specific phase or step.
	Phase string
	// Scope is the primary affected object.
	Scope DistributedTaskScope
	// SourceNode is the node being drained, replaced, or transferred from when applicable.
	SourceNode uint64
	// TargetNode is the node receiving work or leadership when applicable.
	TargetNode uint64
	// OwnerNode is the current task executor owner when known.
	OwnerNode uint64
	// Attempt is the durable retry or attempt counter when known.
	Attempt uint32
	// NextRunAt is the next retry/run time when known.
	NextRunAt *time.Time
	// CreatedAt is the source creation time when known.
	CreatedAt *time.Time
	// UpdatedAt is the source update time when known.
	UpdatedAt *time.Time
	// LastError is the latest source error when known.
	LastError string
	// Summary is a concise operator-facing description.
	Summary string
	// Links contains manager route hints keyed by object type.
	Links map[string]string
}

// DistributedTaskWarning describes a non-fatal source read failure.
type DistributedTaskWarning struct {
	// Domain is the source family that produced the warning.
	Domain DistributedTaskDomain
	// Code is a stable machine-readable warning code.
	Code string
	// Message is the human-readable warning detail.
	Message string
}

// DistributedTaskListResult is one filtered normalized task page.
type DistributedTaskListResult struct {
	// Total is the number of matching tasks before pagination.
	Total int
	// Items contains the current page of normalized tasks.
	Items []DistributedTask
	// NextOffset is the offset to encode into the next cursor.
	NextOffset int
	// HasMore reports whether another page is available.
	HasMore bool
	// Partial reports whether one or more sources failed while others succeeded.
	Partial bool
	// Warnings describes source failures or truncation.
	Warnings []DistributedTaskWarning
}

// DistributedTaskSummary contains normalized task counters.
type DistributedTaskSummary struct {
	// Total is the number of tasks across all available sources.
	Total int
	// ByStatus counts tasks by normalized status.
	ByStatus map[DistributedTaskStatus]int
	// ByDomain counts tasks by source domain.
	ByDomain map[DistributedTaskDomain]int
	// Partial reports whether one or more sources failed while others succeeded.
	Partial bool
	// Warnings describes source failures or truncation.
	Warnings []DistributedTaskWarning
}

// DistributedTaskDetail contains one task and source-specific detail payload.
type DistributedTaskDetail struct {
	// Task is the normalized task row.
	Task DistributedTask
	// Detail contains the source-specific detail.
	Detail DistributedTaskDetailPayload
}

// DistributedTaskDetailPayload contains source-specific task detail data.
type DistributedTaskDetailPayload struct {
	// Domain identifies which detail field is populated.
	Domain DistributedTaskDomain
	// RawStatus is the source lifecycle state before normalization.
	RawStatus string
	// Slot contains Slot reconcile task detail when Domain is slot_reconcile.
	Slot *TaskDetail
	// NodeOnboarding contains onboarding job detail when Domain is node_onboarding.
	NodeOnboarding *NodeOnboardingJob
	// NodeScaleIn contains scale-in report detail when Domain is node_scale_in.
	NodeScaleIn *NodeScaleInReport
	// ChannelMigration contains channel migration detail when Domain is channel_migration.
	ChannelMigration *ChannelMigrationDetail
}

type distributedTaskSource interface {
	domain() DistributedTaskDomain
	list(context.Context) ([]DistributedTask, []DistributedTaskWarning, error)
	get(context.Context, string) (DistributedTaskDetail, error)
}

// ListDistributedTasks returns one normalized read-only distributed task page.
func (a *App) ListDistributedTasks(ctx context.Context, query DistributedTaskQuery) (DistributedTaskListResult, error) {
	if a == nil {
		return DistributedTaskListResult{}, nil
	}
	return aggregateDistributedTasks(ctx, a.distributedTaskSources(), query, a.now())
}

// GetDistributedTasksSummary returns normalized read-only task counts across task domains.
func (a *App) GetDistributedTasksSummary(ctx context.Context) (DistributedTaskSummary, error) {
	if a == nil {
		return newDistributedTaskSummary(), nil
	}
	return aggregateDistributedTaskSummary(ctx, a.distributedTaskSources(), a.now())
}

// GetDistributedTask returns one normalized read-only distributed task detail.
func (a *App) GetDistributedTask(ctx context.Context, domain DistributedTaskDomain, id string) (DistributedTaskDetail, error) {
	if a == nil {
		return DistributedTaskDetail{}, ErrDistributedTaskNotFound
	}
	for _, source := range a.distributedTaskSources() {
		if source.domain() == domain {
			return source.get(ctx, id)
		}
	}
	return DistributedTaskDetail{}, ErrDistributedTaskNotFound
}

func (a *App) distributedTaskSources() []distributedTaskSource {
	if a == nil {
		return nil
	}
	sources := make([]distributedTaskSource, 0, 4)
	if a.cluster != nil {
		sources = append(sources,
			distributedSlotReconcileSource{app: a},
			distributedNodeOnboardingSource{app: a},
			distributedNodeScaleInSource{app: a},
		)
	}
	if a.cluster != nil && a.channelMigration != nil {
		sources = append(sources, distributedChannelMigrationSource{app: a})
	}
	return sources
}

func aggregateDistributedTasks(ctx context.Context, sources []distributedTaskSource, query DistributedTaskQuery, _ time.Time) (DistributedTaskListResult, error) {
	query.Limit = normalizeDistributedTaskLimit(query.Limit)
	if query.Offset < 0 {
		query.Offset = 0
	}
	items, warnings, partial, err := collectDistributedTasks(ctx, sources)
	if err != nil {
		return DistributedTaskListResult{}, err
	}
	filtered := make([]DistributedTask, 0, len(items))
	for _, item := range items {
		if distributedTaskMatchesQuery(item, query) {
			filtered = append(filtered, item)
		}
	}
	sortDistributedTasks(filtered)
	total := len(filtered)
	start := query.Offset
	if start > total {
		start = total
	}
	end := start + query.Limit
	if end > total {
		end = total
	}
	page := append([]DistributedTask(nil), filtered[start:end]...)
	nextOffset := end
	hasMore := end < total
	if !hasMore {
		nextOffset = 0
	}
	return DistributedTaskListResult{
		Total:      total,
		Items:      page,
		NextOffset: nextOffset,
		HasMore:    hasMore,
		Partial:    partial,
		Warnings:   warnings,
	}, nil
}

func aggregateDistributedTaskSummary(ctx context.Context, sources []distributedTaskSource, _ time.Time) (DistributedTaskSummary, error) {
	items, warnings, partial, err := collectDistributedTasks(ctx, sources)
	if err != nil {
		return DistributedTaskSummary{}, err
	}
	summary := newDistributedTaskSummary()
	summary.Partial = partial
	summary.Warnings = warnings
	for _, item := range items {
		summary.Total++
		summary.ByStatus[validDistributedTaskStatus(item.Status)]++
		summary.ByDomain[validDistributedTaskDomain(item.Domain)]++
	}
	return summary, nil
}

func collectDistributedTasks(ctx context.Context, sources []distributedTaskSource) ([]DistributedTask, []DistributedTaskWarning, bool, error) {
	if len(sources) == 0 {
		return nil, nil, false, ErrDistributedTasksUnavailable
	}
	var (
		out      []DistributedTask
		warnings []DistributedTaskWarning
		success  int
	)
	for _, source := range sources {
		items, sourceWarnings, err := source.list(ctx)
		if err != nil {
			warnings = append(warnings, DistributedTaskWarning{
				Domain:  source.domain(),
				Code:    "source_unavailable",
				Message: err.Error(),
			})
			continue
		}
		success++
		out = append(out, items...)
		warnings = append(warnings, sourceWarnings...)
	}
	if success == 0 {
		return nil, warnings, false, ErrDistributedTasksUnavailable
	}
	return out, warnings, len(warnings) > 0, nil
}

func newDistributedTaskSummary() DistributedTaskSummary {
	statuses := []DistributedTaskStatus{
		DistributedTaskStatusPending,
		DistributedTaskStatusRunning,
		DistributedTaskStatusRetrying,
		DistributedTaskStatusBlocked,
		DistributedTaskStatusFailed,
		DistributedTaskStatusCompleted,
		DistributedTaskStatusCancelled,
		DistributedTaskStatusUnknown,
	}
	domains := []DistributedTaskDomain{
		DistributedTaskDomainSlotReconcile,
		DistributedTaskDomainNodeOnboarding,
		DistributedTaskDomainNodeScaleIn,
		DistributedTaskDomainChannelMigration,
	}
	summary := DistributedTaskSummary{
		ByStatus: make(map[DistributedTaskStatus]int, len(statuses)),
		ByDomain: make(map[DistributedTaskDomain]int, len(domains)),
	}
	for _, status := range statuses {
		summary.ByStatus[status] = 0
	}
	for _, domain := range domains {
		summary.ByDomain[domain] = 0
	}
	return summary
}

func normalizeDistributedTaskLimit(limit int) int {
	if limit <= 0 {
		return distributedTaskDefaultLimit
	}
	if limit > distributedTaskMaxLimit {
		return distributedTaskMaxLimit
	}
	return limit
}

func distributedTaskMatchesQuery(task DistributedTask, query DistributedTaskQuery) bool {
	if query.Domain != "" && task.Domain != query.Domain {
		return false
	}
	if query.Status != "" && task.Status != query.Status {
		return false
	}
	if query.Scope != "" && task.Scope.Type != query.Scope {
		return false
	}
	if query.NodeID != 0 && !distributedTaskMentionsNode(task, query.NodeID) {
		return false
	}
	keyword := strings.TrimSpace(strings.ToLower(query.Keyword))
	if keyword != "" && !strings.Contains(strings.ToLower(distributedTaskKeywordText(task)), keyword) {
		return false
	}
	return true
}

func distributedTaskMentionsNode(task DistributedTask, nodeID uint64) bool {
	return task.SourceNode == nodeID ||
		task.TargetNode == nodeID ||
		task.OwnerNode == nodeID ||
		task.Scope.NodeID == nodeID
}

func distributedTaskKeywordText(task DistributedTask) string {
	parts := []string{
		task.ID,
		string(task.Domain),
		task.Kind,
		string(task.Status),
		task.Phase,
		string(task.Scope.Type),
		task.Scope.ID,
		task.Scope.ChannelID,
		task.LastError,
		task.Summary,
		strconv.FormatUint(uint64(task.Scope.SlotID), 10),
		strconv.FormatUint(uint64(task.Scope.ChannelType), 10),
		strconv.FormatUint(task.Scope.NodeID, 10),
		strconv.FormatUint(task.SourceNode, 10),
		strconv.FormatUint(task.TargetNode, 10),
		strconv.FormatUint(task.OwnerNode, 10),
	}
	return strings.Join(parts, " ")
}

func sortDistributedTasks(items []DistributedTask) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if distributedTaskStatusRank(left.Status) != distributedTaskStatusRank(right.Status) {
			return distributedTaskStatusRank(left.Status) < distributedTaskStatusRank(right.Status)
		}
		leftTime, leftOK := distributedTaskSortTime(left)
		rightTime, rightOK := distributedTaskSortTime(right)
		if leftOK != rightOK {
			return leftOK
		}
		if leftOK && !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		if left.Domain != right.Domain {
			return left.Domain < right.Domain
		}
		return left.ID < right.ID
	})
}

func distributedTaskStatusRank(status DistributedTaskStatus) int {
	switch status {
	case DistributedTaskStatusFailed:
		return 0
	case DistributedTaskStatusBlocked:
		return 1
	case DistributedTaskStatusRetrying:
		return 2
	case DistributedTaskStatusRunning:
		return 3
	case DistributedTaskStatusPending:
		return 4
	case DistributedTaskStatusCompleted:
		return 5
	case DistributedTaskStatusCancelled:
		return 6
	default:
		return 7
	}
}

func distributedTaskSortTime(task DistributedTask) (time.Time, bool) {
	for _, ts := range []*time.Time{task.UpdatedAt, task.NextRunAt, task.CreatedAt} {
		if ts != nil && !ts.IsZero() {
			return *ts, true
		}
	}
	return time.Time{}, false
}

func validDistributedTaskStatus(status DistributedTaskStatus) DistributedTaskStatus {
	switch status {
	case DistributedTaskStatusPending,
		DistributedTaskStatusRunning,
		DistributedTaskStatusRetrying,
		DistributedTaskStatusBlocked,
		DistributedTaskStatusFailed,
		DistributedTaskStatusCompleted,
		DistributedTaskStatusCancelled:
		return status
	default:
		return DistributedTaskStatusUnknown
	}
}

func validDistributedTaskDomain(domain DistributedTaskDomain) DistributedTaskDomain {
	switch domain {
	case DistributedTaskDomainSlotReconcile,
		DistributedTaskDomainNodeOnboarding,
		DistributedTaskDomainNodeScaleIn,
		DistributedTaskDomainChannelMigration:
		return domain
	default:
		return DistributedTaskDomain("")
	}
}

type distributedSlotReconcileSource struct{ app *App }

func (s distributedSlotReconcileSource) domain() DistributedTaskDomain {
	return DistributedTaskDomainSlotReconcile
}

func (s distributedSlotReconcileSource) list(ctx context.Context) ([]DistributedTask, []DistributedTaskWarning, error) {
	tasks, err := s.app.ListTasks(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := make([]DistributedTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, distributedTaskFromSlotTask(task))
	}
	return out, nil, nil
}

func (s distributedSlotReconcileSource) get(ctx context.Context, id string) (DistributedTaskDetail, error) {
	slotID, err := parseSlotReconcileDistributedTaskID(id)
	if err != nil {
		return DistributedTaskDetail{}, err
	}
	detail, err := s.app.GetTask(ctx, slotID)
	if err != nil {
		if errors.Is(err, controllermeta.ErrNotFound) {
			return DistributedTaskDetail{}, ErrDistributedTaskNotFound
		}
		return DistributedTaskDetail{}, err
	}
	task := distributedTaskFromSlotTask(detail.Task)
	return DistributedTaskDetail{
		Task: task,
		Detail: DistributedTaskDetailPayload{
			Domain:    DistributedTaskDomainSlotReconcile,
			RawStatus: detail.Status,
			Slot:      &detail,
		},
	}, nil
}

func distributedTaskFromSlotTask(task Task) DistributedTask {
	id := fmt.Sprintf("slot-reconcile:%d", task.SlotID)
	return DistributedTask{
		ID:         id,
		Domain:     DistributedTaskDomainSlotReconcile,
		Kind:       task.Kind,
		Status:     normalizeSlotReconcileStatus(task.Status),
		Phase:      task.Step,
		Scope:      DistributedTaskScope{Type: DistributedTaskScopeSlot, ID: strconv.FormatUint(uint64(task.SlotID), 10), SlotID: task.SlotID},
		SourceNode: task.SourceNode,
		TargetNode: task.TargetNode,
		Attempt:    task.Attempt,
		NextRunAt:  task.NextRunAt,
		LastError:  task.LastError,
		Summary:    fmt.Sprintf("Slot %d %s is %s on %s.", task.SlotID, task.Kind, task.Status, task.Step),
		Links:      map[string]string{"slot": fmt.Sprintf("/slots?slot_id=%d", task.SlotID)},
	}
}

func parseSlotReconcileDistributedTaskID(id string) (uint32, error) {
	raw := strings.TrimPrefix(id, "slot-reconcile:")
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || value == 0 {
		return 0, ErrDistributedTaskNotFound
	}
	return uint32(value), nil
}

func normalizeSlotReconcileStatus(status string) DistributedTaskStatus {
	switch status {
	case "pending":
		return DistributedTaskStatusPending
	case "retrying":
		return DistributedTaskStatusRetrying
	case "failed":
		return DistributedTaskStatusFailed
	default:
		return DistributedTaskStatusUnknown
	}
}

type distributedNodeOnboardingSource struct{ app *App }

func (s distributedNodeOnboardingSource) domain() DistributedTaskDomain {
	return DistributedTaskDomainNodeOnboarding
}

func (s distributedNodeOnboardingSource) list(ctx context.Context) ([]DistributedTask, []DistributedTaskWarning, error) {
	jobs, err := s.app.ListNodeOnboardingJobs(ctx, ListNodeOnboardingJobsRequest{Limit: distributedTaskSourceScanLimit})
	if err != nil {
		return nil, nil, err
	}
	out := make([]DistributedTask, 0, len(jobs.Items))
	for _, job := range jobs.Items {
		out = append(out, distributedTaskFromNodeOnboardingJob(job))
	}
	if jobs.HasMore {
		return out, []DistributedTaskWarning{{
			Domain:  DistributedTaskDomainNodeOnboarding,
			Code:    "source_truncated",
			Message: "node onboarding job scan exceeded task center limit",
		}}, nil
	}
	return out, nil, nil
}

func (s distributedNodeOnboardingSource) get(ctx context.Context, id string) (DistributedTaskDetail, error) {
	resp, err := s.app.GetNodeOnboardingJob(ctx, id)
	if err != nil {
		if errors.Is(err, controllermeta.ErrNotFound) {
			return DistributedTaskDetail{}, ErrDistributedTaskNotFound
		}
		return DistributedTaskDetail{}, err
	}
	task := distributedTaskFromNodeOnboardingJob(resp.Job)
	return DistributedTaskDetail{
		Task: task,
		Detail: DistributedTaskDetailPayload{
			Domain:         DistributedTaskDomainNodeOnboarding,
			RawStatus:      resp.Job.Status,
			NodeOnboarding: &resp.Job,
		},
	}, nil
}

func distributedTaskFromNodeOnboardingJob(job NodeOnboardingJob) DistributedTask {
	return DistributedTask{
		ID:         job.JobID,
		Domain:     DistributedTaskDomainNodeOnboarding,
		Kind:       "onboarding",
		Status:     normalizeNodeOnboardingStatus(job.Status),
		Phase:      nodeOnboardingPhase(job),
		Scope:      DistributedTaskScope{Type: DistributedTaskScopeJob, ID: job.JobID},
		TargetNode: job.TargetNodeID,
		CreatedAt:  timePtrIfSet(job.CreatedAt),
		UpdatedAt:  timePtrIfSet(job.UpdatedAt),
		LastError:  job.LastError,
		Summary:    fmt.Sprintf("Node onboarding job %s is %s.", job.JobID, job.Status),
		Links:      map[string]string{"onboarding": "/onboarding"},
	}
}

func nodeOnboardingPhase(job NodeOnboardingJob) string {
	if job.CurrentMoveIndex >= 0 {
		return fmt.Sprintf("move_%d", job.CurrentMoveIndex)
	}
	return job.Status
}

func normalizeNodeOnboardingStatus(status string) DistributedTaskStatus {
	switch status {
	case "planned":
		return DistributedTaskStatusPending
	case "running":
		return DistributedTaskStatusRunning
	case "failed":
		return DistributedTaskStatusFailed
	case "completed":
		return DistributedTaskStatusCompleted
	case "cancelled":
		return DistributedTaskStatusCancelled
	default:
		return DistributedTaskStatusUnknown
	}
}

type distributedNodeScaleInSource struct{ app *App }

func (s distributedNodeScaleInSource) domain() DistributedTaskDomain {
	return DistributedTaskDomainNodeScaleIn
}

func (s distributedNodeScaleInSource) list(ctx context.Context) ([]DistributedTask, []DistributedTaskWarning, error) {
	nodes, err := s.app.cluster.ListNodesStrict(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := make([]DistributedTask, 0)
	for _, node := range nodes {
		if node.Role != controllermeta.NodeRoleData || node.Status != controllermeta.NodeStatusDraining {
			continue
		}
		report, reportErr := s.app.GetNodeScaleInStatus(ctx, node.NodeID)
		if reportErr != nil {
			return nil, nil, reportErr
		}
		out = append(out, distributedTaskFromNodeScaleInReport(report))
	}
	return out, nil, nil
}

func (s distributedNodeScaleInSource) get(ctx context.Context, id string) (DistributedTaskDetail, error) {
	nodeID, err := parseNodeScaleInDistributedTaskID(id)
	if err != nil {
		return DistributedTaskDetail{}, err
	}
	report, err := s.app.GetNodeScaleInStatus(ctx, nodeID)
	if err != nil {
		return DistributedTaskDetail{}, err
	}
	task := distributedTaskFromNodeScaleInReport(report)
	return DistributedTaskDetail{
		Task: task,
		Detail: DistributedTaskDetailPayload{
			Domain:      DistributedTaskDomainNodeScaleIn,
			RawStatus:   string(report.Status),
			NodeScaleIn: &report,
		},
	}, nil
}

func distributedTaskFromNodeScaleInReport(report NodeScaleInReport) DistributedTask {
	id := fmt.Sprintf("node-scale-in:%d", report.NodeID)
	return DistributedTask{
		ID:         id,
		Domain:     DistributedTaskDomainNodeScaleIn,
		Kind:       "scale_in",
		Status:     normalizeNodeScaleInStatus(report.Status),
		Phase:      nodeScaleInPhase(report),
		Scope:      DistributedTaskScope{Type: DistributedTaskScopeNode, ID: strconv.FormatUint(report.NodeID, 10), NodeID: report.NodeID},
		SourceNode: report.NodeID,
		TargetNode: report.NodeID,
		Summary:    fmt.Sprintf("Node %d scale-in is %s.", report.NodeID, report.Status),
		Links:      map[string]string{"node": "/nodes"},
	}
}

func nodeScaleInPhase(report NodeScaleInReport) string {
	if report.Status == NodeScaleInStatusBlocked && len(report.BlockedReasons) > 0 {
		return report.BlockedReasons[0].Code
	}
	return string(report.Status)
}

func normalizeNodeScaleInStatus(status NodeScaleInStatus) DistributedTaskStatus {
	switch status {
	case NodeScaleInStatusBlocked:
		return DistributedTaskStatusBlocked
	case NodeScaleInStatusFailed:
		return DistributedTaskStatusFailed
	case NodeScaleInStatusReadyToRemove:
		return DistributedTaskStatusCompleted
	case NodeScaleInStatusNotStarted:
		return DistributedTaskStatusPending
	case NodeScaleInStatusMigratingReplicas,
		NodeScaleInStatusTransferringLeaders,
		NodeScaleInStatusWaitingChannelMigrations,
		NodeScaleInStatusDrainingChannels,
		NodeScaleInStatusWaitingConnections:
		return DistributedTaskStatusRunning
	default:
		return DistributedTaskStatusUnknown
	}
}

func parseNodeScaleInDistributedTaskID(id string) (uint64, error) {
	raw := strings.TrimPrefix(id, "node-scale-in:")
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, ErrDistributedTaskNotFound
	}
	return value, nil
}

type distributedChannelMigrationSource struct{ app *App }

func (s distributedChannelMigrationSource) domain() DistributedTaskDomain {
	return DistributedTaskDomainChannelMigration
}

func (s distributedChannelMigrationSource) list(ctx context.Context) ([]DistributedTask, []DistributedTaskWarning, error) {
	nodes, err := s.app.cluster.ListNodesStrict(ctx)
	if err != nil {
		return nil, nil, err
	}
	seen := make(map[string]metadb.ChannelMigrationTask)
	for _, node := range nodes {
		if node.Role != controllermeta.NodeRoleData || node.JoinState != controllermeta.NodeJoinStateActive {
			continue
		}
		tasks, hasMore, err := s.app.channelMigration.ListActiveChannelMigrationTasksForNode(ctx, node.NodeID, distributedTaskSourceScanLimit)
		if err != nil {
			return nil, nil, err
		}
		for _, task := range tasks {
			seen[channelMigrationDistributedTaskKey(task)] = task
		}
		if hasMore {
			return distributedTasksFromChannelMigrationMap(seen), []DistributedTaskWarning{{
				Domain:  DistributedTaskDomainChannelMigration,
				Code:    "source_truncated",
				Message: "channel migration scan exceeded task center limit",
			}}, nil
		}
	}
	return distributedTasksFromChannelMigrationMap(seen), nil, nil
}

func (s distributedChannelMigrationSource) get(ctx context.Context, id string) (DistributedTaskDetail, error) {
	channelID, channelType, taskID, err := decodeChannelMigrationDistributedTaskID(id)
	if err != nil {
		return DistributedTaskDetail{}, err
	}
	detail, err := s.app.GetChannelMigration(ctx, channel.ChannelID{ID: channelID, Type: uint8(channelType)})
	if err != nil {
		if errors.Is(err, metadb.ErrNotFound) {
			return DistributedTaskDetail{}, ErrDistributedTaskNotFound
		}
		return DistributedTaskDetail{}, err
	}
	if detail.TaskID != taskID {
		return DistributedTaskDetail{}, ErrDistributedTaskNotFound
	}
	task := distributedTaskFromChannelMigrationDetail(detail)
	return DistributedTaskDetail{
		Task: task,
		Detail: DistributedTaskDetailPayload{
			Domain:           DistributedTaskDomainChannelMigration,
			RawStatus:        detail.Status,
			ChannelMigration: &detail,
		},
	}, nil
}

func distributedTasksFromChannelMigrationMap(tasks map[string]metadb.ChannelMigrationTask) []DistributedTask {
	out := make([]DistributedTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, distributedTaskFromChannelMigrationTask(task))
	}
	return out
}

func distributedTaskFromChannelMigrationTask(task metadb.ChannelMigrationTask) DistributedTask {
	detail := ChannelMigrationDetail{
		TaskID:        task.TaskID,
		Kind:          managerChannelMigrationKind(task.Kind),
		Status:        managerChannelMigrationStatus(task.Status),
		Phase:         managerChannelMigrationPhase(task.Phase),
		ChannelID:     task.ChannelID,
		ChannelType:   task.ChannelType,
		SourceNode:    task.SourceNode,
		TargetNode:    task.TargetNode,
		DesiredLeader: task.DesiredLeader,
		Attempt:       task.Attempt,
		NextRunAtMS:   task.NextRunAtMS,
		LastError:     task.LastError,
		CreatedAtMS:   task.CreatedAtMS,
		UpdatedAtMS:   task.UpdatedAtMS,
		CompletedAtMS: task.CompletedAtMS,
	}
	return distributedTaskFromChannelMigrationDetail(detail)
}

func distributedTaskFromChannelMigrationDetail(detail ChannelMigrationDetail) DistributedTask {
	id := encodeChannelMigrationDistributedTaskID(detail.ChannelID, detail.ChannelType, detail.TaskID)
	return DistributedTask{
		ID:         id,
		Domain:     DistributedTaskDomainChannelMigration,
		Kind:       detail.Kind,
		Status:     normalizeChannelMigrationStatus(detail.Status),
		Phase:      detail.Phase,
		Scope:      DistributedTaskScope{Type: DistributedTaskScopeChannel, ID: fmt.Sprintf("%d/%s", detail.ChannelType, detail.ChannelID), ChannelID: detail.ChannelID, ChannelType: detail.ChannelType},
		SourceNode: detail.SourceNode,
		TargetNode: detail.TargetNode,
		OwnerNode:  0,
		Attempt:    detail.Attempt,
		NextRunAt:  timePtrFromUnixMS(detail.NextRunAtMS),
		CreatedAt:  timePtrFromUnixMS(detail.CreatedAtMS),
		UpdatedAt:  timePtrFromUnixMS(detail.UpdatedAtMS),
		LastError:  detail.LastError,
		Summary:    fmt.Sprintf("Channel %d/%s %s is %s on %s.", detail.ChannelType, detail.ChannelID, detail.Kind, detail.Status, detail.Phase),
		Links:      map[string]string{"channel": fmt.Sprintf("/channel-cluster/list?channel_id=%s", detail.ChannelID)},
	}
}

func channelMigrationDistributedTaskKey(task metadb.ChannelMigrationTask) string {
	return fmt.Sprintf("%d\x00%s\x00%s", task.ChannelType, task.ChannelID, task.TaskID)
}

type channelMigrationDistributedTaskID struct {
	Version     int    `json:"v"`
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
	TaskID      string `json:"task_id"`
}

func encodeChannelMigrationDistributedTaskID(channelID string, channelType int64, taskID string) string {
	payload, _ := json.Marshal(channelMigrationDistributedTaskID{
		Version:     distributedTaskChannelIDVersion,
		ChannelID:   channelID,
		ChannelType: channelType,
		TaskID:      taskID,
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeChannelMigrationDistributedTaskID(id string) (string, int64, string, error) {
	payload, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return "", 0, "", ErrDistributedTaskNotFound
	}
	var body channelMigrationDistributedTaskID
	if err := json.Unmarshal(payload, &body); err != nil {
		return "", 0, "", ErrDistributedTaskNotFound
	}
	if body.Version != distributedTaskChannelIDVersion || strings.TrimSpace(body.ChannelID) == "" || body.ChannelType <= 0 || strings.TrimSpace(body.TaskID) == "" {
		return "", 0, "", ErrDistributedTaskNotFound
	}
	return body.ChannelID, body.ChannelType, body.TaskID, nil
}

func normalizeChannelMigrationStatus(status string) DistributedTaskStatus {
	switch status {
	case "pending":
		return DistributedTaskStatusPending
	case "running":
		return DistributedTaskStatusRunning
	case "blocked":
		return DistributedTaskStatusBlocked
	case "completed":
		return DistributedTaskStatusCompleted
	case "failed":
		return DistributedTaskStatusFailed
	case "aborted", "cancelled":
		return DistributedTaskStatusCancelled
	default:
		return DistributedTaskStatusUnknown
	}
}

func timePtrIfSet(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	v := t
	return &v
}

func timePtrFromUnixMS(ms int64) *time.Time {
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}
