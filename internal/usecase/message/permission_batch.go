package message

import (
	"context"
	"fmt"

	channelmembers "github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// permissionFacts exists for one sealed admission batch only. It evaluates the
// same scalar policy without any network fallback or cross-request cache.
type permissionFacts struct {
	indexes map[PermissionRead]int
	results []PermissionReadResult
}

func (f *permissionFacts) get(q PermissionRead) (PermissionReadResult, error) {
	i, ok := f.indexes[q]
	if !ok || i >= len(f.results) {
		return PermissionReadResult{}, fmt.Errorf("message: missing permission fact")
	}
	r := f.results[i]
	return r, r.Err
}
func (f *permissionFacts) GetUserSendPolicy(_ context.Context, uid string) (metadb.SendBanResult, error) {
	r, err := f.get(PermissionRead{Kind: PermissionReadUserSendPolicy, UID: uid})
	return r.UserPolicy, err
}
func (f *permissionFacts) GetChannelForPermission(_ context.Context, id string, kind int64) (metadb.Channel, error) {
	r, err := f.get(PermissionRead{Kind: PermissionReadChannel, ChannelID: id, ChannelType: kind})
	if err != nil {
		return metadb.Channel{}, err
	}
	if !r.Found {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return r.Channel, nil
}
func (f *permissionFacts) ContainsChannelSubscriber(_ context.Context, id string, kind int64, uid string) (bool, error) {
	r, err := f.get(PermissionRead{Kind: PermissionReadSubscriberContains, ChannelID: id, ChannelType: kind, UID: uid})
	return r.Value, err
}
func (f *permissionFacts) HasChannelSubscribers(_ context.Context, id string, kind int64) (bool, error) {
	r, err := f.get(PermissionRead{Kind: PermissionReadSubscriberHasAny, ChannelID: id, ChannelType: kind})
	return r.Value, err
}

// checkSendPermissionsBatch collects user, source-Channel and auxiliary facts in
// one plan, including mixed channel types and explicit-recipient commands.
func (a *App) checkSendPermissionsBatch(ctx context.Context, items []SendBatchItem, groups []sendBatchPermissionGroup, indexes []int) []sendBatchPermissionOutcome {
	const maxPlanGroups = 512 // at most six facts per ordinary group, below the 4096 fact cap
	if len(indexes) > maxPlanGroups {
		out := make([]sendBatchPermissionOutcome, 0, len(indexes))
		for start := 0; start < len(indexes); start += maxPlanGroups {
			out = append(out, a.checkSendPermissionsBatch(ctx, items, groups, indexes[start:min(start+maxPlanGroups, len(indexes))])...)
		}
		return out
	}
	planStarted := a.permissionStart()
	beforeCount := 0
	reads := make([]PermissionRead, 0, len(indexes)*6)
	facts := &permissionFacts{indexes: make(map[PermissionRead]int, len(indexes)*6)}
	add := func(q PermissionRead) {
		beforeCount++
		if _, ok := facts.indexes[q]; !ok {
			facts.indexes[q] = len(reads)
			reads = append(reads, q)
		}
	}
	channel := func(id string, kind uint8) {
		add(PermissionRead{Kind: PermissionReadChannel, ChannelID: id, ChannelType: int64(kind)})
	}
	member := func(id string, kind uint8, uid string) {
		add(PermissionRead{Kind: PermissionReadSubscriberContains, ChannelID: id, ChannelType: int64(kind), UID: uid})
	}
	common := func(id string, kind uint8, uid string) {
		k := channelmembers.ChannelKey{ChannelID: id, ChannelType: kind}
		member(channelmembers.DenylistChannelID(k), kind, uid)
		member(id, kind, uid)
		allow := channelmembers.AllowlistChannelID(k)
		add(PermissionRead{Kind: PermissionReadSubscriberHasAny, ChannelID: allow, ChannelType: int64(kind)})
		member(allow, kind, uid)
	}
	out := make([]sendBatchPermissionOutcome, len(indexes))
	sources := make([]string, len(indexes))
	for i, index := range indexes {
		cmd := items[groups[index].representative].Command
		out[i].channelID = cmd.ChannelID
		add(PermissionRead{Kind: PermissionReadUserSendPolicy, UID: cmd.FromUID})
		if cmd.RequestScoped || (len(cmd.MessageScopedUIDs) > 0 && cmd.ChannelID == "") {
			continue
		}
		id, _ := a.commandChannels.FromCommandChannel(cmd.ChannelID)
		if cmd.ChannelType == channelTypePerson && cmd.NormalizePersonChannel {
			var err error
			id, err = runtimechannelid.NormalizePersonChannel(cmd.FromUID, id)
			if err != nil {
				out[i].err = err
				continue
			}
		}
		sources[i] = id
		channel(id, cmd.ChannelType)
		if a.systemUIDs != nil && a.systemUIDs.IsSystemUID(cmd.FromUID) || a.systemDeviceID != "" && cmd.DeviceID == a.systemDeviceID {
			continue
		}
		switch cmd.ChannelType {
		case channelTypeGroup:
			common(id, cmd.ChannelType, cmd.FromUID)
		case channelTypeVisitors:
			if cmd.FromUID != id {
				common(id, channelTypeCustomerService, cmd.FromUID)
			}
		case channelTypePerson:
			left, right, err := runtimechannelid.DecodePersonChannel(id)
			if err != nil {
				out[i].err = err
				continue
			}
			receiver := right
			if cmd.FromUID == right {
				receiver = left
			}
			if a.systemUIDs != nil && a.systemUIDs.IsSystemUID(receiver) {
				continue
			}
			key := channelmembers.ChannelKey{ChannelID: receiver, ChannelType: channelTypePerson}
			member(channelmembers.DenylistChannelID(key), channelTypePerson, cmd.FromUID)
			if a.personWhitelistEnabled {
				member(channelmembers.AllowlistChannelID(key), channelTypePerson, cmd.FromUID)
				channel(receiver, channelTypePerson)
			}
		}
	}
	users, channels, messages := 0, 0, 0
	for _, q := range reads {
		switch q.Kind {
		case PermissionReadUserSendPolicy:
			users++
		case PermissionReadChannel:
			channels++
		}
	}
	for _, i := range indexes {
		messages += max(1, len(groups[i].indexes))
	}
	a.permissionCount("messages", messages)
	a.permissionCount("users", users)
	a.permissionCount("channels", channels)
	a.permissionCount("facts_before", beforeCount)
	a.permissionCount("facts", len(reads))
	a.permissionStage("plan", "ok", planStarted)
	facts.results = a.readPermissionFacts(ctx, reads)
	if len(facts.results) != len(reads) {
		err := fmt.Errorf("message: permission batch returned %d facts for %d reads", len(facts.results), len(reads))
		for i := range out {
			out[i].reason = ReasonSystemError
			out[i].err = err
		}
		return out
	}
	evaluateStarted := a.permissionStart()
	defer a.permissionStage("evaluate", "ok", evaluateStarted)
	evaluator := *a
	evaluator.permissionObserver = nil
	evaluator.permissionBatch = nil
	evaluator.permissions = facts
	evaluator.permissionAuthority = facts
	for i, index := range indexes {
		if out[i].err != nil {
			continue
		}
		item := items[groups[index].representative]
		if item.Context != nil && item.Context.Err() != nil {
			out[i].reason = ReasonSystemError
			out[i].err = item.Context.Err()
			continue
		}
		cmd, reason, err := evaluator.checkSendPermission(ctx, item.Command)
		out[i].channelID, out[i].reason, out[i].err = cmd.ChannelID, reason, err
		if err == nil && reason == ReasonSendBan {
			scope := "channel"
			policy, policyErr := facts.GetUserSendPolicy(ctx, item.Command.FromUID)
			if policyErr == nil && policy.SendBan != 0 {
				scope = "user"
			}
			a.observeSendBan(scope, max(1, len(groups[index].indexes)))
		}
		if item.Command.ChannelType == channelTypePerson && sources[i] != "" {
			r, e := facts.get(PermissionRead{Kind: PermissionReadChannel, ChannelID: sources[i], ChannelType: int64(channelTypePerson)})
			if e == nil {
				out[i].personDirectoryFact = &PersonDirectoryChannelFact{Found: r.Found, Channel: r.Channel}
			}
		}
	}
	return out
}

// readPermissionFacts merges cache misses into the same fresh read as mandatory
// user/channel policy. Only auxiliary set facts can cross request boundaries.
func (a *App) readPermissionFacts(ctx context.Context, reads []PermissionRead) []PermissionReadResult {
	cache, ok := a.permissions.(*permissionCache)
	if !ok {
		return a.permissionBatch.ReadPermissionsBatch(ctx, reads)
	}
	out := make([]PermissionReadResult, len(reads))
	misses := make([]PermissionRead, 0, len(reads))
	indexes := make([]int, 0, len(reads))
	generations := make([]uint64, len(reads))
	now := cache.now()
	for i, q := range reads {
		hit := false
		switch q.Kind {
		case PermissionReadSubscriberContains:
			value, err, found, generation := permissionCacheGet(cache, cache.contains, permissionCacheContainsKey{q.ChannelID, q.ChannelType, q.UID}, now)
			out[i] = PermissionReadResult{Value: value, Err: err}
			hit = found
			generations[i] = generation
		case PermissionReadSubscriberHasAny:
			value, err, found, generation := permissionCacheGet(cache, cache.hasAny, permissionCacheChannelKey{q.ChannelID, q.ChannelType}, now)
			out[i] = PermissionReadResult{Value: value, Err: err}
			hit = found
			generations[i] = generation
		}
		if !hit {
			indexes = append(indexes, i)
			misses = append(misses, q)
		}
	}
	fresh := a.permissionBatch.ReadPermissionsBatch(ctx, misses)
	if len(fresh) != len(misses) {
		return nil
	}
	for j, i := range indexes {
		out[i] = fresh[j]
		if out[i].Err != nil {
			continue
		}
		q := reads[i]
		switch q.Kind {
		case PermissionReadSubscriberContains:
			permissionCachePut(cache, cache.contains, permissionCacheContainsKey{q.ChannelID, q.ChannelType, q.UID}, out[i].Value, nil, now.Add(cache.ttl), generations[i])
		case PermissionReadSubscriberHasAny:
			permissionCachePut(cache, cache.hasAny, permissionCacheChannelKey{q.ChannelID, q.ChannelType}, out[i].Value, nil, now.Add(cache.ttl), generations[i])
		}
	}
	return out
}
