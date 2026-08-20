package message

import (
	"context"
	"errors"
	"sort"
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

type sendBatchDirectoryOutcome struct {
	groupIndex int
	err        error
}

type personDirectoryReadiness interface {
	PersonChannelDirectoryReady(channelID string, channelType int64) bool
}

// sendBatchEachSubmitter optionally publishes terminal channel-group results
// before unrelated groups in the same batch complete. Implementations must
// serialize emit calls and join all admitted work before returning.
type sendBatchEachSubmitter interface {
	SendBatchEach([]SendBatchItem, func(int, SendBatchItemResult))
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

// SendBatch checks send permissions and returns one result aligned with each
// input. Latency-sensitive adapters should use SendBatchEach so an already
// completed hot result is not held behind unrelated cold preparation.
func (a *App) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	results := make([]SendBatchItemResult, len(items))
	_ = a.SendBatchEach(items, func(index int, result SendBatchItemResult) error {
		results[index] = result
		return nil
	})
	return results
}

// SendBatchEach synchronously emits each item result as soon as it becomes
// final. Indexes may arrive out of input order, emit is never called
// concurrently, and the method does not return until all internal work has
// joined. The first emitter error stops later emissions but not internal joins.
func (a *App) SendBatchEach(items []SendBatchItem, emit func(int, SendBatchItemResult) error) error {
	if emit == nil {
		return ErrSendBatchEmitterRequired
	}
	permissionStartedAt := time.Now()
	results := make([]SendBatchItemResult, len(items))
	finalized := make([]bool, len(items))
	var emitErr error
	var invariantErr error
	finalize := func(index int) {
		if index < 0 || index >= len(results) || finalized[index] {
			invariantErr = ErrSendBatchEmissionMismatch
			return
		}
		finalized[index] = true
		if emitErr == nil {
			emitErr = emit(index, results[index])
		}
	}
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
	for i := range items {
		if !allowedItems[i] {
			finalize(i)
		}
	}

	preAppendStartedAt := time.Now()
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
	readiness, _ := a.personDirectory.(personDirectoryReadiness)
	coldItems := make([]bool, len(items))
	coldDirectoryGroups := make([]sendBatchDirectoryGroup, 0, len(directoryGroups))
	for _, group := range directoryGroups {
		item := prepared[group.representative]
		if readiness != nil && readiness.PersonChannelDirectoryReady(item.Command.ChannelID, int64(item.Command.ChannelType)) {
			continue
		}
		coldDirectoryGroups = append(coldDirectoryGroups, group)
		for _, index := range group.indexes {
			coldItems[index] = true
		}
	}
	immediateIndexes := make([]int, 0, len(items))
	for i := range items {
		if allowedItems[i] && !coldItems[i] {
			immediateIndexes = append(immediateIndexes, i)
		}
	}
	directoryOutcomes := make(chan sendBatchDirectoryOutcome, len(coldDirectoryGroups))
	directoryWorkers := min(len(coldDirectoryGroups), sendBatchPreAppendWorkers)
	var nextDirectory atomic.Uint64
	for range directoryWorkers {
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskMessageDirectoryBatch, func() {
			for {
				groupIndex := int(nextDirectory.Add(1) - 1)
				if groupIndex >= len(coldDirectoryGroups) {
					return
				}
				group := coldDirectoryGroups[groupIndex]
				item := prepared[group.representative]
				directoryOutcomes <- sendBatchDirectoryOutcome{
					groupIndex: groupIndex,
					err:        a.ensurePersonDirectory(contexts[group.representative], item.Command),
				}
			}
		})
	}
	if len(immediateIndexes) > 0 {
		a.submitSendBatchLane(prepared, contexts, allowedItems, immediateIndexes, results, permissionDuration, preAppendStartedAt, sendBatchStageResultOK, finalize)
	}
	for completed := 0; completed < len(coldDirectoryGroups); {
		wave := []sendBatchDirectoryOutcome{<-directoryOutcomes}
		completed++
	drainCompleted:
		for completed < len(coldDirectoryGroups) {
			select {
			case outcome := <-directoryOutcomes:
				wave = append(wave, outcome)
				completed++
			default:
				break drainCompleted
			}
		}
		wavePreAppendResult := sendBatchStageResultOK
		waveIndexes := make([]int, 0, len(wave))
		for _, outcome := range wave {
			group := coldDirectoryGroups[outcome.groupIndex]
			waveIndexes = append(waveIndexes, group.indexes...)
			if outcome.err == nil {
				continue
			}
			wavePreAppendResult = sendBatchStageResultErr
			for _, index := range group.indexes {
				results[index] = SendBatchItemResult{Result: SendResult{Reason: ReasonSystemError}, Err: outcome.err}
				allowedItems[index] = false
			}
		}
		sort.Ints(waveIndexes)
		a.submitSendBatchLane(prepared, contexts, allowedItems, waveIndexes, results, permissionDuration, preAppendStartedAt, wavePreAppendResult, finalize)
	}
	for i := range finalized {
		if !finalized[i] {
			invariantErr = ErrSendBatchEmissionMismatch
			break
		}
	}
	return errors.Join(emitErr, invariantErr)
}

func (a *App) submitSendBatchLane(
	prepared []SendBatchItem,
	contexts []context.Context,
	allowedItems []bool,
	indexes []int,
	results []SendBatchItemResult,
	permissionDuration time.Duration,
	preAppendStartedAt time.Time,
	preAppendResult string,
	finalize func(int),
) {
	allowed := make([]SendBatchItem, 0, len(indexes))
	allowedIndexes := make([]int, 0, len(indexes))
	for _, i := range indexes {
		if !allowedItems[i] {
			finalize(i)
			continue
		}
		item := prepared[i]
		ctx := contexts[i]
		cmd := item.Command
		cmd, reason, err := a.beforeSendHook(ctx, cmd)
		if err != nil {
			results[i] = SendBatchItemResult{Result: SendResult{Reason: reason}, Err: err}
			preAppendResult = sendBatchStageResultErr
			finalize(i)
			continue
		}
		if reason != ReasonSuccess {
			results[i] = SendBatchItemResult{Result: SendResult{Reason: reason}}
			finalize(i)
			continue
		}
		item.Command = cmd
		allowed = append(allowed, item)
		allowedIndexes = append(allowedIndexes, i)
	}
	preAppendDuration := time.Since(preAppendStartedAt)
	a.observeSendBatchStage(sendBatchStagePreAppend, preAppendResult, len(indexes), preAppendDuration)
	if len(allowed) == 0 {
		return
	}
	submitterStartedAt := time.Now()
	if a == nil || a.submitter == nil {
		for allowedIndex, index := range allowedIndexes {
			results[index].Err = annotateSendBatchTimeout(ErrRouteNotReady, SendBatchFailureDiagnostics{
				FailedStage: sendBatchStageSubmitter, Permission: permissionDuration, PreAppend: preAppendDuration,
				DeadlineBudgetBeforeSubmit: sendBatchDeadlineBudget(allowed[allowedIndex].Deadline, submitterStartedAt),
			})
		}
		a.observeSendBatchStage(sendBatchStageSubmitter, sendBatchStageResultErr, len(allowed), time.Since(submitterStartedAt))
		for _, index := range allowedIndexes {
			finalize(index)
		}
		return
	}
	if streaming, ok := a.submitter.(sendBatchEachSubmitter); ok {
		emitted := make([]bool, len(allowed))
		submitterResult := sendBatchStageResultOK
		streaming.SendBatchEach(allowed, func(index int, result SendBatchItemResult) {
			if index < 0 || index >= len(allowed) || emitted[index] {
				submitterResult = sendBatchStageResultErr
				return
			}
			emitted[index] = true
			itemDuration := time.Since(submitterStartedAt)
			if result.Err != nil {
				submitterResult = sendBatchStageResultErr
			}
			result.Err = annotateSendBatchTimeout(result.Err, SendBatchFailureDiagnostics{
				FailedStage: sendBatchStageSubmitter, Permission: permissionDuration, PreAppend: preAppendDuration,
				Submitter:                  itemDuration,
				DeadlineBudgetBeforeSubmit: sendBatchDeadlineBudget(allowed[index].Deadline, submitterStartedAt),
			})
			originalIndex := allowedIndexes[index]
			results[originalIndex] = result
			finalize(originalIndex)
		})
		submitterDuration := time.Since(submitterStartedAt)
		for index, wasEmitted := range emitted {
			if wasEmitted {
				continue
			}
			submitterResult = sendBatchStageResultErr
			originalIndex := allowedIndexes[index]
			results[originalIndex].Err = annotateSendBatchTimeout(ErrSendBatchEmissionMismatch, SendBatchFailureDiagnostics{
				FailedStage: sendBatchStageSubmitter, Permission: permissionDuration, PreAppend: preAppendDuration,
				Submitter:                  submitterDuration,
				DeadlineBudgetBeforeSubmit: sendBatchDeadlineBudget(allowed[index].Deadline, submitterStartedAt),
			})
			finalize(originalIndex)
		}
		a.observeSendBatchStage(sendBatchStageSubmitter, submitterResult, len(allowed), submitterDuration)
		return
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
		if i >= len(allowedIndexes) {
			break
		}
		result.Err = annotateSendBatchTimeout(result.Err, SendBatchFailureDiagnostics{
			FailedStage: sendBatchStageSubmitter, Permission: permissionDuration, PreAppend: preAppendDuration,
			Submitter:                  submitterDuration,
			DeadlineBudgetBeforeSubmit: sendBatchDeadlineBudget(allowed[i].Deadline, submitterStartedAt),
		})
		results[allowedIndexes[i]] = result
	}
	for _, index := range allowedIndexes {
		finalize(index)
	}
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
