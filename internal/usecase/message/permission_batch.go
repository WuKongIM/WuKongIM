package message

import (
	"context"
	"fmt"

	channelmembers "github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

type groupPermissionReadPlan struct {
	command        SendCommand
	trusted        bool
	senderChannel  int
	groupChannel   int
	denied         int
	subscriber     int
	hasAllowlist   int
	allowlistEntry int
}

type personPermissionReadPlan struct {
	command         SendCommand
	planErr         error
	trusted         bool
	systemDevice    bool
	receiverTrusted bool
	senderChannel   int
	terminalChannel int
	denied          int
	allowlistEntry  int
	receiverChannel int
}

func (a *App) checkGroupSendPermissionsBatch(
	ctx context.Context,
	items []SendBatchItem,
	groups []sendBatchPermissionGroup,
	groupIndexes []int,
) []sendBatchPermissionOutcome {
	reads := make([]PermissionRead, 0, len(groupIndexes)*5)
	readIndexes := make(map[PermissionRead]int, len(groupIndexes)*5)
	addRead := func(read PermissionRead) int {
		if index, ok := readIndexes[read]; ok {
			return index
		}
		index := len(reads)
		readIndexes[read] = index
		reads = append(reads, read)
		return index
	}
	plans := make([]groupPermissionReadPlan, len(groupIndexes))
	for i, groupIndex := range groupIndexes {
		cmd := items[groups[groupIndex].representative].Command
		sourceChannelID, _ := runtimechannelid.FromCommandChannel(cmd.ChannelID)
		channelType := int64(cmd.ChannelType)
		plan := groupPermissionReadPlan{
			command:        cmd,
			senderChannel:  -1,
			groupChannel:   -1,
			denied:         -1,
			subscriber:     -1,
			hasAllowlist:   -1,
			allowlistEntry: -1,
		}
		plan.groupChannel = addRead(PermissionRead{
			Kind: PermissionReadChannel, ChannelID: sourceChannelID, ChannelType: channelType,
		})
		plan.trusted = a.systemUIDs != nil && a.systemUIDs.IsSystemUID(cmd.FromUID)
		if !plan.trusted {
			plan.senderChannel = addRead(PermissionRead{
				Kind: PermissionReadChannel, ChannelID: cmd.FromUID, ChannelType: int64(channelTypePerson),
			})
			if a.systemDeviceID != "" && cmd.DeviceID == a.systemDeviceID {
				plan.trusted = true
			} else {
				key := channelmembers.ChannelKey{ChannelID: sourceChannelID, ChannelType: cmd.ChannelType}
				allowID := channelmembers.AllowlistChannelID(key)
				plan.denied = addRead(PermissionRead{
					Kind: PermissionReadSubscriberContains, ChannelID: channelmembers.DenylistChannelID(key), ChannelType: channelType, UID: cmd.FromUID,
				})
				plan.subscriber = addRead(PermissionRead{
					Kind: PermissionReadSubscriberContains, ChannelID: sourceChannelID, ChannelType: channelType, UID: cmd.FromUID,
				})
				plan.hasAllowlist = addRead(PermissionRead{
					Kind: PermissionReadSubscriberHasAny, ChannelID: allowID, ChannelType: channelType,
				})
				// Read the point entry in the same authoritative round. Evaluation
				// ignores it when the allowlist is empty.
				plan.allowlistEntry = addRead(PermissionRead{
					Kind: PermissionReadSubscriberContains, ChannelID: allowID, ChannelType: channelType, UID: cmd.FromUID,
				})
			}
		}
		plans[i] = plan
	}

	readResults := a.permissionBatch.ReadPermissionsBatch(ctx, reads)
	if len(readResults) != len(reads) {
		err := fmt.Errorf("message: permission batch returned %d results for %d reads", len(readResults), len(reads))
		outcomes := make([]sendBatchPermissionOutcome, len(plans))
		for i, plan := range plans {
			outcomes[i] = sendBatchPermissionOutcome{channelID: plan.command.ChannelID, reason: ReasonSystemError, err: err}
		}
		return outcomes
	}

	outcomes := make([]sendBatchPermissionOutcome, len(plans))
	for i, plan := range plans {
		outcomes[i] = evaluateGroupPermissionReadPlan(plan, readResults)
	}
	return outcomes
}

func evaluateGroupPermissionReadPlan(plan groupPermissionReadPlan, results []PermissionReadResult) sendBatchPermissionOutcome {
	outcome := sendBatchPermissionOutcome{channelID: plan.command.ChannelID, reason: ReasonSuccess}
	read := func(index int) (PermissionReadResult, bool) {
		if index < 0 {
			return PermissionReadResult{}, false
		}
		return results[index], true
	}
	if sender, ok := read(plan.senderChannel); ok {
		if sender.Err != nil {
			outcome.reason, outcome.err = ReasonSystemError, sender.Err
			return outcome
		}
		if sender.Found && sender.Channel.SendBan != 0 {
			outcome.reason = ReasonSendBan
			return outcome
		}
	}
	group, _ := read(plan.groupChannel)
	if group.Err != nil {
		outcome.reason, outcome.err = ReasonSystemError, group.Err
		return outcome
	}
	if !group.Found {
		if !plan.trusted {
			outcome.reason = ReasonChannelNotExist
		}
		return outcome
	}
	if plan.trusted {
		if group.Channel.Disband != 0 {
			outcome.reason = ReasonDisband
		}
		return outcome
	}
	if group.Channel.Ban != 0 {
		outcome.reason = ReasonBan
		return outcome
	}
	if group.Channel.Disband != 0 {
		outcome.reason = ReasonDisband
		return outcome
	}
	denied, _ := read(plan.denied)
	if denied.Err != nil {
		outcome.reason, outcome.err = ReasonSystemError, denied.Err
		return outcome
	}
	if denied.Value {
		outcome.reason = ReasonInBlacklist
		return outcome
	}
	subscriber, _ := read(plan.subscriber)
	if subscriber.Err != nil {
		outcome.reason, outcome.err = ReasonSystemError, subscriber.Err
		return outcome
	}
	if !subscriber.Value {
		outcome.reason = ReasonSubscriberNotExist
		return outcome
	}
	hasAllowlist, _ := read(plan.hasAllowlist)
	if hasAllowlist.Err != nil {
		outcome.reason, outcome.err = ReasonSystemError, hasAllowlist.Err
		return outcome
	}
	if !hasAllowlist.Value {
		return outcome
	}
	allowlistEntry, _ := read(plan.allowlistEntry)
	if allowlistEntry.Err != nil {
		outcome.reason, outcome.err = ReasonSystemError, allowlistEntry.Err
		return outcome
	}
	if !allowlistEntry.Value {
		outcome.reason = ReasonNotInWhitelist
	}
	return outcome
}

func (a *App) checkPersonSendPermissionsBatch(
	ctx context.Context,
	items []SendBatchItem,
	groups []sendBatchPermissionGroup,
	groupIndexes []int,
) []sendBatchPermissionOutcome {
	reads := make([]PermissionRead, 0, len(groupIndexes)*5)
	readIndexes := make(map[PermissionRead]int, len(groupIndexes)*5)
	addRead := func(read PermissionRead) int {
		if index, ok := readIndexes[read]; ok {
			return index
		}
		index := len(reads)
		readIndexes[read] = index
		reads = append(reads, read)
		return index
	}
	plans := make([]personPermissionReadPlan, len(groupIndexes))
	for i, groupIndex := range groupIndexes {
		cmd := items[groups[groupIndex].representative].Command
		sourceChannelID, commandChannel := runtimechannelid.FromCommandChannel(cmd.ChannelID)
		cmd.ChannelID = sourceChannelID
		if cmd.NormalizePersonChannel {
			normalized, err := runtimechannelid.NormalizePersonChannel(cmd.FromUID, cmd.ChannelID)
			if err != nil {
				plans[i] = personPermissionReadPlan{command: cmd, planErr: err}
				continue
			}
			cmd.ChannelID = normalized
		}
		if commandChannel {
			cmd.ChannelID = runtimechannelid.ToCommandChannel(cmd.ChannelID)
		}
		plan := personPermissionReadPlan{
			command:         cmd,
			senderChannel:   -1,
			terminalChannel: -1,
			denied:          -1,
			allowlistEntry:  -1,
			receiverChannel: -1,
		}
		permissionChannelID, _ := runtimechannelid.FromCommandChannel(cmd.ChannelID)
		plan.terminalChannel = addRead(PermissionRead{
			Kind: PermissionReadChannel, ChannelID: permissionChannelID, ChannelType: int64(channelTypePerson),
		})
		plan.trusted = a.systemUIDs != nil && a.systemUIDs.IsSystemUID(cmd.FromUID)
		if plan.trusted {
			plans[i] = plan
			continue
		}
		plan.senderChannel = addRead(PermissionRead{
			Kind: PermissionReadChannel, ChannelID: cmd.FromUID, ChannelType: int64(channelTypePerson),
		})
		plan.systemDevice = a.systemDeviceID != "" && cmd.DeviceID == a.systemDeviceID
		if plan.systemDevice {
			plans[i] = plan
			continue
		}
		left, right, err := runtimechannelid.DecodePersonChannel(permissionChannelID)
		if err != nil {
			plan.planErr = err
			plans[i] = plan
			continue
		}
		receiver := right
		if cmd.FromUID == right {
			receiver = left
		}
		plan.receiverTrusted = a.systemUIDs != nil && a.systemUIDs.IsSystemUID(receiver)
		if plan.receiverTrusted {
			plans[i] = plan
			continue
		}
		key := channelmembers.ChannelKey{ChannelID: receiver, ChannelType: channelTypePerson}
		plan.denied = addRead(PermissionRead{
			Kind: PermissionReadSubscriberContains, ChannelID: channelmembers.DenylistChannelID(key), ChannelType: int64(channelTypePerson), UID: cmd.FromUID,
		})
		if a.personWhitelistEnabled {
			plan.allowlistEntry = addRead(PermissionRead{
				Kind: PermissionReadSubscriberContains, ChannelID: channelmembers.AllowlistChannelID(key), ChannelType: int64(channelTypePerson), UID: cmd.FromUID,
			})
			plan.receiverChannel = addRead(PermissionRead{
				Kind: PermissionReadChannel, ChannelID: receiver, ChannelType: int64(channelTypePerson),
			})
		}
		plans[i] = plan
	}

	readResults := a.permissionBatch.ReadPermissionsBatch(ctx, reads)
	if len(readResults) != len(reads) {
		err := fmt.Errorf("message: permission batch returned %d results for %d reads", len(readResults), len(reads))
		outcomes := make([]sendBatchPermissionOutcome, len(plans))
		for i, plan := range plans {
			outcomes[i] = sendBatchPermissionOutcome{channelID: plan.command.ChannelID, reason: ReasonSystemError, err: err}
		}
		return outcomes
	}

	outcomes := make([]sendBatchPermissionOutcome, len(plans))
	for i, plan := range plans {
		outcome := evaluatePersonPermissionReadPlan(plan, readResults)
		if plan.terminalChannel >= 0 && plan.terminalChannel < len(readResults) {
			terminal := readResults[plan.terminalChannel]
			if terminal.Err == nil {
				outcome.personDirectoryFact = &PersonDirectoryChannelFact{
					Found: terminal.Found, Channel: terminal.Channel,
				}
			}
		}
		outcomes[i] = outcome
	}
	return outcomes
}

func evaluatePersonPermissionReadPlan(plan personPermissionReadPlan, results []PermissionReadResult) sendBatchPermissionOutcome {
	outcome := sendBatchPermissionOutcome{channelID: plan.command.ChannelID, reason: ReasonSuccess}
	if plan.planErr != nil {
		outcome.err = plan.planErr
		return outcome
	}
	read := func(index int) (PermissionReadResult, bool) {
		if index < 0 {
			return PermissionReadResult{}, false
		}
		return results[index], true
	}
	if sender, ok := read(plan.senderChannel); ok {
		if sender.Err != nil {
			outcome.reason, outcome.err = ReasonSystemError, sender.Err
			return outcome
		}
		if sender.Found && sender.Channel.SendBan != 0 {
			outcome.reason = ReasonSendBan
			return outcome
		}
	}
	terminal, _ := read(plan.terminalChannel)
	if terminal.Err != nil {
		outcome.reason, outcome.err = ReasonSystemError, terminal.Err
		return outcome
	}
	if terminal.Found && terminal.Channel.Disband != 0 {
		outcome.reason = ReasonDisband
		return outcome
	}
	if plan.trusted || plan.systemDevice || plan.receiverTrusted {
		return outcome
	}
	denied, _ := read(plan.denied)
	if denied.Err != nil {
		outcome.reason, outcome.err = ReasonSystemError, denied.Err
		return outcome
	}
	if denied.Value {
		outcome.reason = ReasonInBlacklist
		return outcome
	}
	allowlistEntry, ok := read(plan.allowlistEntry)
	if !ok {
		return outcome
	}
	if allowlistEntry.Err != nil {
		outcome.reason, outcome.err = ReasonSystemError, allowlistEntry.Err
		return outcome
	}
	if allowlistEntry.Value {
		return outcome
	}
	receiver, _ := read(plan.receiverChannel)
	if receiver.Err != nil {
		outcome.reason, outcome.err = ReasonSystemError, receiver.Err
		return outcome
	}
	if !receiver.Found || receiver.Channel.AllowStranger == 0 {
		outcome.reason = ReasonNotInWhitelist
	}
	return outcome
}
