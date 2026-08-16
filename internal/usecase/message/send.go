package message

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const (
	channelTypePerson          uint8 = 1
	channelTypeGroup           uint8 = 2
	channelTypeCustomerService uint8 = 3
	channelTypeInfo            uint8 = 6
	channelTypeVisitors        uint8 = 10
	channelTypeAgent           uint8 = 11

	// sendBatchPermissionWorkers bounds concurrent authoritative metadata reads
	// without changing the original item order used by hooks and append admission.
	sendBatchPermissionWorkers = 16
	// sendBatchPreAppendWorkers matches the bounded person-directory batch size.
	// A full caller wave closes that downstream batch immediately instead of
	// paying its collection deadline in groups of a few entries.
	sendBatchPreAppendWorkers = 128

	sendBatchStagePermission = "permission"
	sendBatchStagePreAppend  = "pre_append"
	sendBatchStageSubmitter  = "submitter"
	sendBatchStageResultOK   = "ok"
	sendBatchStageResultErr  = "error"
)

// sendPermissionScope contains only fields that can change send authorization.
// Payload and per-message identity deliberately stay outside the key so a
// session batch can share one permission snapshot without merging commands.
type sendPermissionScope struct {
	fromUID                string
	deviceID               string
	channelID              string
	channelType            uint8
	normalizePersonChannel bool
}

type sendBatchPermissionGroup struct {
	representative int
	indexes        []int
}

type sendBatchPermissionOutcome struct {
	channelID string
	reason    Reason
	err       error
}

type sendBatchDirectoryGroup struct {
	representative int
	indexes        []int
}

// Send checks send permissions and delegates one allowed command to the configured channel append submitter.
func (a *App) Send(ctx context.Context, cmd SendCommand) (SendResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd, reason, err := a.checkSendPermission(ctx, cmd)
	if err != nil {
		return SendResult{Reason: reason}, err
	}
	if reason != ReasonSuccess {
		return SendResult{Reason: reason}, nil
	}
	cmd, reason, err = a.beforeSendHook(ctx, cmd)
	if err != nil {
		return SendResult{Reason: reason}, err
	}
	if reason != ReasonSuccess {
		return SendResult{Reason: reason}, nil
	}
	if err := a.ensurePersonDirectory(ctx, cmd); err != nil {
		return SendResult{Reason: ReasonSystemError}, err
	}
	if a == nil || a.submitter == nil {
		return SendResult{}, ErrRouteNotReady
	}
	return a.submitter.Send(ctx, cmd)
}

// SendBatch checks send permissions and delegates allowed commands to the configured channel append submitter.
func (a *App) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	permissionStartedAt := time.Now()
	results := make([]SendBatchItemResult, len(items))
	prepared := make([]SendBatchItem, len(items))
	contexts := make([]context.Context, len(items))
	allowedItems := make([]bool, len(items))
	permissionGroups := make([]sendBatchPermissionGroup, 0, len(items))
	permissionGroupByScope := make(map[sendPermissionScope]int, len(items))
	for i := range items {
		scope, coalescible := permissionScopeForBatch(items[i].Command)
		if !coalescible {
			permissionGroups = append(permissionGroups, sendBatchPermissionGroup{representative: i, indexes: []int{i}})
			continue
		}
		groupIndex, ok := permissionGroupByScope[scope]
		if !ok {
			groupIndex = len(permissionGroups)
			permissionGroupByScope[scope] = groupIndex
			permissionGroups = append(permissionGroups, sendBatchPermissionGroup{representative: i})
		}
		permissionGroups[groupIndex].indexes = append(permissionGroups[groupIndex].indexes, i)
	}
	permissionWorkers := sendBatchPermissionWorkers
	if a == nil || a.permissions == nil {
		permissionWorkers = 1
	}
	permissionOutcomes := a.resolveSendBatchPermissions(items, permissionGroups, permissionWorkers)
	for groupIndex, group := range permissionGroups {
		outcome := permissionOutcomes[groupIndex]
		for _, i := range group.indexes {
			item := items[i]
			itemCtx := item.Context
			if itemCtx == nil {
				itemCtx = context.Background()
			}
			if outcome.err != nil {
				results[i] = SendBatchItemResult{Result: SendResult{Reason: outcome.reason}, Err: outcome.err}
				continue
			}
			if outcome.reason != ReasonSuccess {
				results[i] = SendBatchItemResult{Result: SendResult{Reason: outcome.reason}}
				continue
			}
			// Permission normalization currently changes only ChannelID. Preserve
			// each item's payload, identity, deadline, and hook metadata.
			item.Command.ChannelID = outcome.channelID
			prepared[i] = item
			contexts[i] = itemCtx
			allowedItems[i] = true
		}
	}
	permissionResult := sendBatchStageResultOK
	for _, outcome := range permissionOutcomes {
		if outcome.err != nil {
			permissionResult = sendBatchStageResultErr
			break
		}
	}
	permissionDuration := time.Since(permissionStartedAt)
	a.observeSendBatchStage(sendBatchStagePermission, permissionResult, len(items), permissionDuration)

	preAppendStartedAt := time.Now()
	preAppendResult := sendBatchStageResultOK
	directoryGroups := make([]sendBatchDirectoryGroup, 0, len(items))
	directoryGroupByChannel := make(map[string]int, len(items))
	if a != nil && a.personDirectory != nil {
		for i := range prepared {
			if !allowedItems[i] || !sendNeedsPersonDirectory(prepared[i].Command) {
				continue
			}
			channelID := prepared[i].Command.ChannelID
			groupIndex, ok := directoryGroupByChannel[channelID]
			if !ok {
				groupIndex = len(directoryGroups)
				directoryGroupByChannel[channelID] = groupIndex
				directoryGroups = append(directoryGroups, sendBatchDirectoryGroup{representative: i})
			}
			directoryGroups[groupIndex].indexes = append(directoryGroups[groupIndex].indexes, i)
		}
	}
	directoryErrors := make([]error, len(directoryGroups))
	runSendBatchWorkers(goruntimeregistry.TaskMessageDirectoryBatch, len(directoryGroups), sendBatchPreAppendWorkers, func(groupIndex int) {
		group := directoryGroups[groupIndex]
		item := prepared[group.representative]
		directoryErrors[groupIndex] = a.ensurePersonDirectory(contexts[group.representative], item.Command)
	})
	for groupIndex, group := range directoryGroups {
		if directoryErrors[groupIndex] == nil {
			continue
		}
		preAppendResult = sendBatchStageResultErr
		for _, index := range group.indexes {
			results[index] = SendBatchItemResult{Result: SendResult{Reason: ReasonSystemError}, Err: directoryErrors[groupIndex]}
			allowedItems[index] = false
		}
	}
	allowed := make([]SendBatchItem, 0, len(items))
	indexes := make([]int, 0, len(items))
	for i, item := range prepared {
		if !allowedItems[i] {
			continue
		}
		ctx := contexts[i]
		cmd := item.Command
		cmd, reason, err := a.beforeSendHook(ctx, cmd)
		if err != nil {
			results[i] = SendBatchItemResult{Result: SendResult{Reason: reason}, Err: err}
			preAppendResult = sendBatchStageResultErr
			continue
		}
		if reason != ReasonSuccess {
			results[i] = SendBatchItemResult{Result: SendResult{Reason: reason}}
			continue
		}
		item.Command = cmd
		allowed = append(allowed, item)
		indexes = append(indexes, i)
	}
	preAppendDuration := time.Since(preAppendStartedAt)
	a.observeSendBatchStage(sendBatchStagePreAppend, preAppendResult, len(items), preAppendDuration)
	if len(allowed) == 0 {
		return results
	}
	submitterStartedAt := time.Now()
	if a == nil || a.submitter == nil {
		for allowedIndex, index := range indexes {
			results[index].Err = annotateSendBatchTimeout(ErrRouteNotReady, SendBatchFailureDiagnostics{
				FailedStage: sendBatchStageSubmitter, Permission: permissionDuration, PreAppend: preAppendDuration,
				DeadlineBudgetBeforeSubmit: sendBatchDeadlineBudget(allowed[allowedIndex].Deadline, submitterStartedAt),
			})
		}
		a.observeSendBatchStage(sendBatchStageSubmitter, sendBatchStageResultErr, len(allowed), time.Since(submitterStartedAt))
		return results
	}
	delegated := a.submitter.SendBatch(allowed)
	submitterDuration := time.Since(submitterStartedAt)
	submitterResult := sendBatchStageResultOK
	if len(delegated) != len(allowed) {
		submitterResult = sendBatchStageResultErr
	} else {
		for _, result := range delegated {
			if result.Err != nil {
				submitterResult = sendBatchStageResultErr
				break
			}
		}
	}
	a.observeSendBatchStage(sendBatchStageSubmitter, submitterResult, len(allowed), submitterDuration)
	for i, result := range delegated {
		if i >= len(indexes) {
			break
		}
		result.Err = annotateSendBatchTimeout(result.Err, SendBatchFailureDiagnostics{
			FailedStage: sendBatchStageSubmitter, Permission: permissionDuration, PreAppend: preAppendDuration,
			Submitter:                  submitterDuration,
			DeadlineBudgetBeforeSubmit: sendBatchDeadlineBudget(allowed[i].Deadline, submitterStartedAt),
		})
		results[indexes[i]] = result
	}
	return results
}

func sendBatchDeadlineBudget(deadline, submitterStartedAt time.Time) time.Duration {
	if deadline.IsZero() {
		return 0
	}
	return deadline.Sub(submitterStartedAt)
}

func (a *App) observeSendBatchStage(stage, result string, items int, duration time.Duration) {
	if a == nil || a.sendBatchObserver == nil {
		return
	}
	a.sendBatchObserver.ObserveMessageSendBatchStage(SendBatchStageObservation{
		Stage: stage, Result: result, Items: items, Duration: duration,
	})
}

func (a *App) resolveSendBatchPermissions(items []SendBatchItem, groups []sendBatchPermissionGroup, permissionWorkers int) []sendBatchPermissionOutcome {
	outcomes := make([]sendBatchPermissionOutcome, len(groups))
	batchedGroups := make([]int, 0, len(groups))
	fallbackGroups := make([]int, 0, len(groups))
	for groupIndex, group := range groups {
		cmd := items[group.representative].Command
		if a != nil && a.permissionBatch != nil && cmd.ChannelType == channelTypeGroup && !cmd.RequestScoped && len(cmd.MessageScopedUIDs) == 0 {
			batchedGroups = append(batchedGroups, groupIndex)
			continue
		}
		fallbackGroups = append(fallbackGroups, groupIndex)
	}
	if len(batchedGroups) > 0 {
		ctx := items[groups[batchedGroups[0]].representative].Context
		if ctx == nil {
			ctx = context.Background()
		}
		batched := a.checkGroupSendPermissionsBatch(ctx, items, groups, batchedGroups)
		for i, groupIndex := range batchedGroups {
			outcomes[groupIndex] = batched[i]
		}
	}
	runSendBatchWorkers(goruntimeregistry.TaskMessagePermissionBatch, len(fallbackGroups), permissionWorkers, func(index int) {
		groupIndex := fallbackGroups[index]
		item := items[groups[groupIndex].representative]
		ctx := item.Context
		if ctx == nil {
			ctx = context.Background()
		}
		cmd, reason, err := a.checkSendPermission(ctx, item.Command)
		outcomes[groupIndex] = sendBatchPermissionOutcome{channelID: cmd.ChannelID, reason: reason, err: err}
	})
	return outcomes
}

func permissionScopeForBatch(cmd SendCommand) (sendPermissionScope, bool) {
	// Request-scoped delivery can depend on the complete target slice and is
	// already permission-free. Keep it out of ordinary permission coalescing.
	if cmd.RequestScoped || len(cmd.MessageScopedUIDs) > 0 {
		return sendPermissionScope{}, false
	}
	return sendPermissionScope{
		fromUID:                cmd.FromUID,
		deviceID:               cmd.DeviceID,
		channelID:              cmd.ChannelID,
		channelType:            cmd.ChannelType,
		normalizePersonChannel: cmd.NormalizePersonChannel,
	}, true
}

func runSendBatchWorkers(taskID goruntimeregistry.TaskID, workItems int, maxWorkers int, run func(int)) {
	workers := min(workItems, maxWorkers)
	if workers <= 1 {
		for index := 0; index < workItems; index++ {
			run(index)
		}
		return
	}
	var next atomic.Uint64
	worker := func() {
		for {
			index := int(next.Add(1) - 1)
			if index >= workItems {
				return
			}
			run(index)
		}
	}
	var wait sync.WaitGroup
	wait.Add(workers - 1)
	for range workers - 1 {
		goruntimeregistry.SafeGo(nil, taskID, func() {
			defer wait.Done()
			worker()
		})
	}
	worker()
	wait.Wait()
}

func (a *App) ensurePersonDirectory(ctx context.Context, cmd SendCommand) error {
	if a == nil || a.personDirectory == nil || !sendNeedsPersonDirectory(cmd) {
		return nil
	}
	return a.personDirectory.EnsurePersonChannelDirectory(ctx, cmd.ChannelID, int64(cmd.ChannelType))
}

func sendNeedsPersonDirectory(cmd SendCommand) bool {
	return cmd.ChannelType == channelTypePerson && !cmd.NoPersist && !cmd.SyncOnce && !cmd.RequestScoped
}

func (a *App) beforeSendHook(ctx context.Context, cmd SendCommand) (SendCommand, Reason, error) {
	if cmd.SkipPluginHooks || a == nil || a.sendHook == nil {
		return cmd, ReasonSuccess, nil
	}
	if cmd.Origin == "" {
		cmd.Origin = SendOriginClient
	}
	if cmd.Origin == SendOriginPlugin {
		if cmd.HookDepth >= DefaultPluginSendMaxHookDepth {
			return cmd, ReasonSystemError, ErrSendHookDepthExceeded
		}
		cmd.HookDepth++
	}
	mutated, reason, err := a.sendHook.BeforeSend(ctx, cmd)
	if err != nil {
		if reason != 0 {
			return mutated, reason, err
		}
		return mutated, ReasonSystemError, err
	}
	if reason != 0 && reason != ReasonSuccess {
		return mutated, reason, nil
	}
	return mutated, ReasonSuccess, nil
}
