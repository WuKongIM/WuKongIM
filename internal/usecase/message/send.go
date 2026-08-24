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

// sendBatchPermissionGroupKey prevents one item's deadline from becoming the
// representative context for otherwise coalescible items with another budget.
type sendBatchPermissionGroupKey struct {
	scope       sendPermissionScope
	deadline    time.Time
	hasDeadline bool
}

type sendBatchPermissionGroup struct {
	representative int
	indexes        []int
}

type sendBatchPermissionOutcome struct {
	channelID           string
	reason              Reason
	err                 error
	personDirectoryFact *PersonDirectoryChannelFact
}

type sendBatchPermissionDeadlineCohort struct {
	ctx          context.Context
	deadline     time.Time
	hasDeadline  bool
	groupIndexes []int
}

type sendBatchDirectoryGroup struct {
	representative int
	indexes        []int
}

type sendBatchDirectoryOutcome struct {
	groupIndex int
	err        error
}

// sendBatchSessionKey keeps admission and result publication ordered for one
// real gateway session. Non-session callers receive an isolated lane per item.
type sendBatchSessionKey struct {
	nodeID    uint64
	sessionID uint64
	isolated  int
}

func sendBatchSessionKeyFor(index int, cmd SendCommand) sendBatchSessionKey {
	if cmd.SenderNodeID != 0 && cmd.SenderSessionID != 0 {
		return sendBatchSessionKey{nodeID: cmd.SenderNodeID, sessionID: cmd.SenderSessionID}
	}
	return sendBatchSessionKey{isolated: index + 1}
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
	items, cancelItemDeadlines := bindSendBatchItemDeadlines(items)
	defer cancelItemDeadlines()
	permissionStartedAt := time.Now()
	results := make([]SendBatchItemResult, len(items))
	terminal := make([]bool, len(items))
	nextInSession := make([]int, len(items))
	for i := range nextInSession {
		nextInSession[i] = -1
	}
	sessionKeys := make([]sendBatchSessionKey, len(items))
	emissionHeads := make(map[sendBatchSessionKey]int, len(items))
	submissionHeads := make(map[sendBatchSessionKey]int, len(items))
	tails := make(map[sendBatchSessionKey]int, len(items))
	for i, item := range items {
		key := sendBatchSessionKeyFor(i, item.Command)
		sessionKeys[i] = key
		if tail, ok := tails[key]; ok {
			nextInSession[tail] = i
		} else {
			emissionHeads[key] = i
			submissionHeads[key] = i
		}
		tails[key] = i
	}
	var emitErr error
	var invariantErr error
	finalize := func(index int) {
		if index < 0 || index >= len(results) || terminal[index] {
			invariantErr = ErrSendBatchEmissionMismatch
			return
		}
		terminal[index] = true
		key := sessionKeys[index]
		for head := emissionHeads[key]; head >= 0 && terminal[head]; head = emissionHeads[key] {
			if emitErr == nil {
				emitErr = emit(head, results[head])
			}
			emissionHeads[key] = nextInSession[head]
		}
	}
	prepared := make([]SendBatchItem, len(items))
	contexts := make([]context.Context, len(items))
	allowedItems := make([]bool, len(items))
	directoryFacts := make([]*PersonDirectoryChannelFact, len(items))
	permissionGroups := make([]sendBatchPermissionGroup, 0, len(items))
	permissionGroupByScope := make(map[sendBatchPermissionGroupKey]int, len(items))
	for i := range items {
		scope, coalescible := permissionScopeForBatch(items[i].Command)
		if !coalescible {
			permissionGroups = append(permissionGroups, sendBatchPermissionGroup{representative: i, indexes: []int{i}})
			continue
		}
		key := sendBatchPermissionGroupKey{scope: scope}
		itemCtx := items[i].Context
		if itemCtx == nil {
			itemCtx = context.Background()
		}
		if deadline, ok := itemCtx.Deadline(); ok {
			key.deadline = deadline.Round(0).UTC()
			key.hasDeadline = true
		}
		groupIndex, ok := permissionGroupByScope[key]
		if !ok {
			groupIndex = len(permissionGroups)
			permissionGroupByScope[key] = groupIndex
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
			directoryFacts[i] = outcome.personDirectoryFact
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
	readyForSubmit := make([]bool, len(items))
	submitted := make([]bool, len(items))
	drainReady := func(preAppendResult string) {
		for {
			indexes := make([]int, 0, len(submissionHeads))
			for key, head := range submissionHeads {
				for head >= 0 && terminal[head] {
					head = nextInSession[head]
					submissionHeads[key] = head
				}
				if head >= 0 && readyForSubmit[head] && !submitted[head] {
					indexes = append(indexes, head)
				}
			}
			if len(indexes) == 0 {
				return
			}
			sort.Ints(indexes)
			for _, index := range indexes {
				submitted[index] = true
			}
			a.submitSendBatchLane(prepared, contexts, allowedItems, indexes, results, permissionDuration, preAppendStartedAt, preAppendResult, finalize)
		}
	}
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
	coldItems := make([]bool, len(items))
	coldDirectoryGroups := make([]sendBatchDirectoryGroup, 0, len(directoryGroups))
	for _, group := range directoryGroups {
		coldDirectoryGroups = append(coldDirectoryGroups, group)
		for _, index := range group.indexes {
			coldItems[index] = true
		}
	}
	for i := range items {
		if allowedItems[i] && !coldItems[i] {
			readyForSubmit[i] = true
		}
	}
	drainReady(sendBatchStageResultOK)
	if len(coldDirectoryGroups) > 0 {
		submitDirectoryWave := func(wave []sendBatchDirectoryOutcome) {
			wavePreAppendResult := sendBatchStageResultOK
			for _, outcome := range wave {
				group := coldDirectoryGroups[outcome.groupIndex]
				if outcome.err == nil {
					for _, index := range group.indexes {
						readyForSubmit[index] = true
					}
					continue
				}
				wavePreAppendResult = sendBatchStageResultErr
				for _, index := range group.indexes {
					results[index] = SendBatchItemResult{Result: SendResult{Reason: ReasonSystemError}, Err: outcome.err}
					allowedItems[index] = false
					finalize(index)
				}
			}
			drainReady(wavePreAppendResult)
		}
		if waves, ok := a.personDirectory.(PersonDirectoryWaveEnsurer); ok {
			admissions := personDirectoryAdmissions(prepared, contexts, directoryFacts, coldDirectoryGroups)
			completed := make([]bool, len(admissions))
			waves.AdmitPersonChannelDirectoryWaves(admissions, func(admissionWave []PersonDirectoryAdmissionOutcome) {
				wave := make([]sendBatchDirectoryOutcome, 0, len(admissionWave))
				for _, outcome := range admissionWave {
					if outcome.Index < 0 || outcome.Index >= len(completed) || completed[outcome.Index] {
						invariantErr = ErrSendBatchEmissionMismatch
						continue
					}
					completed[outcome.Index] = true
					wave = append(wave, sendBatchDirectoryOutcome{groupIndex: outcome.Index, err: outcome.Err})
				}
				if len(wave) > 0 {
					submitDirectoryWave(wave)
				}
			})
			missing := make([]sendBatchDirectoryOutcome, 0)
			for groupIndex, done := range completed {
				if !done {
					invariantErr = ErrSendBatchEmissionMismatch
					missing = append(missing, sendBatchDirectoryOutcome{groupIndex: groupIndex, err: ErrRouteNotReady})
				}
			}
			if len(missing) > 0 {
				submitDirectoryWave(missing)
			}
		} else {
			submitDirectoryWave(a.admitPersonDirectoryGroups(prepared, contexts, directoryFacts, coldDirectoryGroups))
		}
	}
	for i := range terminal {
		if !terminal[i] {
			invariantErr = ErrSendBatchEmissionMismatch
			break
		}
	}
	return errors.Join(emitErr, invariantErr)
}

// bindSendBatchItemDeadlines keeps entry contexts unchanged at the adapter
// boundary, then derives only the shorter child contexts needed to bound the
// complete message pipeline. The copied slice prevents the usecase from
// replacing contexts in caller-owned batch storage.
func bindSendBatchItemDeadlines(items []SendBatchItem) ([]SendBatchItem, func()) {
	boundCount := 0
	for i := range items {
		deadline := items[i].Deadline
		if deadline.IsZero() {
			continue
		}
		parent := items[i].Context
		if parent == nil {
			parent = context.Background()
		}
		if current, ok := parent.Deadline(); !ok || deadline.Before(current) {
			boundCount++
		}
	}
	if boundCount == 0 {
		return items, func() {}
	}
	bounded := append([]SendBatchItem(nil), items...)
	cancels := make([]context.CancelFunc, 0, boundCount)
	for i := range bounded {
		deadline := bounded[i].Deadline
		if deadline.IsZero() {
			continue
		}
		parent := bounded[i].Context
		if parent == nil {
			parent = context.Background()
		}
		if current, ok := parent.Deadline(); ok && !deadline.Before(current) {
			continue
		}
		var cancel context.CancelFunc
		bounded[i].Context, cancel = context.WithDeadline(parent, deadline)
		cancels = append(cancels, cancel)
	}
	return bounded, func() {
		for i := len(cancels) - 1; i >= 0; i-- {
			cancels[i]()
		}
	}
}

func (a *App) admitPersonDirectoryGroups(items []SendBatchItem, contexts []context.Context, directoryFacts []*PersonDirectoryChannelFact, groups []sendBatchDirectoryGroup) []sendBatchDirectoryOutcome {
	outcomes := make([]sendBatchDirectoryOutcome, len(groups))
	if batch, ok := a.personDirectory.(PersonDirectoryBatchEnsurer); ok {
		admissions := personDirectoryAdmissions(items, contexts, directoryFacts, groups)
		results := batch.AdmitPersonChannelDirectories(admissions)
		for i := range outcomes {
			outcomes[i].groupIndex = i
			if len(results) != len(admissions) {
				outcomes[i].err = ErrRouteNotReady
			} else {
				outcomes[i].err = results[i]
			}
		}
		return outcomes
	}
	for i, group := range groups {
		item := items[group.representative]
		outcomes[i] = sendBatchDirectoryOutcome{
			groupIndex: i,
			err:        a.ensurePersonDirectory(contexts[group.representative], item.Command),
		}
	}
	return outcomes
}

func personDirectoryAdmissions(items []SendBatchItem, contexts []context.Context, directoryFacts []*PersonDirectoryChannelFact, groups []sendBatchDirectoryGroup) []PersonDirectoryAdmission {
	admissions := make([]PersonDirectoryAdmission, len(groups))
	for i, group := range groups {
		item := items[group.representative]
		admissions[i] = PersonDirectoryAdmission{
			Context: contexts[group.representative], ChannelID: item.Command.ChannelID, ChannelType: int64(item.Command.ChannelType),
			ChannelFact: directoryFacts[group.representative],
		}
	}
	return admissions
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
		for allowedIndex, originalIndex := range allowedIndexes {
			results[originalIndex] = SendBatchItemResult{
				Result: SendResult{Reason: ReasonSystemError},
				Err: annotateSendBatchTimeout(ErrSendBatchEmissionMismatch, SendBatchFailureDiagnostics{
					FailedStage: sendBatchStageSubmitter, Permission: permissionDuration, PreAppend: preAppendDuration,
					Submitter:                  submitterDuration,
					DeadlineBudgetBeforeSubmit: sendBatchDeadlineBudget(allowed[allowedIndex].Deadline, submitterStartedAt),
				}),
			}
		}
	} else {
		for _, result := range delegated {
			if result.Err != nil {
				submitterResult = sendBatchStageResultErr
				break
			}
		}
	}
	a.observeSendBatchStage(sendBatchStageSubmitter, submitterResult, len(allowed), submitterDuration)
	if len(delegated) == len(allowed) {
		for i, result := range delegated {
			result.Err = annotateSendBatchTimeout(result.Err, SendBatchFailureDiagnostics{
				FailedStage: sendBatchStageSubmitter, Permission: permissionDuration, PreAppend: preAppendDuration,
				Submitter:                  submitterDuration,
				DeadlineBudgetBeforeSubmit: sendBatchDeadlineBudget(allowed[i].Deadline, submitterStartedAt),
			})
			results[allowedIndexes[i]] = result
		}
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
	batchedPersons := make([]int, 0, len(groups))
	fallbackGroups := make([]int, 0, len(groups))
	for groupIndex, group := range groups {
		cmd := items[group.representative].Command
		if a != nil && a.permissionBatch != nil && !cmd.RequestScoped && len(cmd.MessageScopedUIDs) == 0 {
			switch cmd.ChannelType {
			case channelTypeGroup:
				batchedGroups = append(batchedGroups, groupIndex)
				continue
			case channelTypePerson:
				batchedPersons = append(batchedPersons, groupIndex)
				continue
			}
		}
		fallbackGroups = append(fallbackGroups, groupIndex)
	}
	if len(batchedGroups) > 0 {
		if ctx, shared := sharedSendBatchPermissionContext(items, groups, batchedGroups); shared {
			batched := a.checkGroupSendPermissionsBatch(ctx, items, groups, batchedGroups)
			for i, groupIndex := range batchedGroups {
				outcomes[groupIndex] = batched[i]
			}
		} else {
			for _, cohort := range sendBatchPermissionDeadlineCohorts(items, groups, batchedGroups) {
				batched := a.checkGroupSendPermissionsBatch(cohort.ctx, items, groups, cohort.groupIndexes)
				for i, groupIndex := range cohort.groupIndexes {
					outcomes[groupIndex] = batched[i]
				}
			}
		}
	}
	if len(batchedPersons) > 0 {
		if ctx, shared := sharedSendBatchPermissionContext(items, groups, batchedPersons); shared {
			batched := a.checkPersonSendPermissionsBatch(ctx, items, groups, batchedPersons)
			for i, groupIndex := range batchedPersons {
				outcomes[groupIndex] = batched[i]
			}
		} else {
			for _, cohort := range sendBatchPermissionDeadlineCohorts(items, groups, batchedPersons) {
				batched := a.checkPersonSendPermissionsBatch(cohort.ctx, items, groups, cohort.groupIndexes)
				for i, groupIndex := range cohort.groupIndexes {
					outcomes[groupIndex] = batched[i]
				}
			}
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

func sharedSendBatchPermissionContext(items []SendBatchItem, groups []sendBatchPermissionGroup, groupIndexes []int) (context.Context, bool) {
	ctx := items[groups[groupIndexes[0]].representative].Context
	if ctx == nil {
		ctx = context.Background()
	}
	deadline, hasDeadline := ctx.Deadline()
	for _, groupIndex := range groupIndexes[1:] {
		candidate := items[groups[groupIndex].representative].Context
		if candidate == nil {
			candidate = context.Background()
		}
		candidateDeadline, candidateHasDeadline := candidate.Deadline()
		if candidateHasDeadline != hasDeadline || hasDeadline && !candidateDeadline.Equal(deadline) {
			return nil, false
		}
	}
	return ctx, true
}

func sendBatchPermissionDeadlineCohorts(items []SendBatchItem, groups []sendBatchPermissionGroup, groupIndexes []int) []sendBatchPermissionDeadlineCohort {
	cohorts := make([]sendBatchPermissionDeadlineCohort, 0, 1)
	for _, groupIndex := range groupIndexes {
		ctx := items[groups[groupIndex].representative].Context
		if ctx == nil {
			ctx = context.Background()
		}
		deadline, hasDeadline := ctx.Deadline()
		cohortIndex := -1
		for i := range cohorts {
			if cohorts[i].hasDeadline == hasDeadline && (!hasDeadline || cohorts[i].deadline.Equal(deadline)) {
				cohortIndex = i
				break
			}
		}
		if cohortIndex < 0 {
			cohortIndex = len(cohorts)
			cohorts = append(cohorts, sendBatchPermissionDeadlineCohort{
				ctx: ctx, deadline: deadline, hasDeadline: hasDeadline,
			})
		}
		cohorts[cohortIndex].groupIndexes = append(cohorts[cohortIndex].groupIndexes, groupIndex)
	}
	sort.SliceStable(cohorts, func(i, j int) bool {
		if cohorts[i].hasDeadline != cohorts[j].hasDeadline {
			return cohorts[i].hasDeadline
		}
		return cohorts[i].hasDeadline && cohorts[i].deadline.Before(cohorts[j].deadline)
	})
	return cohorts
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
	return a.personDirectory.AdmitPersonChannelDirectory(ctx, cmd.ChannelID, int64(cmd.ChannelType))
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
