package replica

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func (r *replica) Fetch(ctx context.Context, req channel.ReplicaFetchRequest) (channel.ReplicaFetchResult, error) {
	progress := r.submitLoopCommand(ctx, machineFetchProgressCommand{Request: req})
	if progress.Err != nil {
		return channel.ReplicaFetchResult{}, progress.Err
	}
	if progress.Fetch == nil {
		return channel.ReplicaFetchResult{}, channel.ErrCorruptState
	}
	result := progress.Fetch.Result

	r.appendLogger().Debug("leader served fetch",
		wklog.Event("repl.diag.leader_served_fetch"),
		wklog.String("channelKey", string(progress.Fetch.ChannelKey)),
		wklog.Uint64("replicaID", uint64(req.ReplicaID)),
		wklog.Uint64("fetchOffset", req.FetchOffset),
		wklog.Uint64("matchOffset", progress.Fetch.MatchOffset),
		wklog.Uint64("oldProgress", progress.Fetch.OldProgress),
		wklog.Uint64("leaderLEO", progress.Fetch.LeaderLEO),
		wklog.Uint64("hw", result.HW),
		wklog.Bool("needsAdvance", progress.Fetch.NeedsAdvance),
	)
	if progress.Fetch.ReadLog == nil {
		return result, nil
	}

	effect := *progress.Fetch.ReadLog
	records, readErr := r.log.Read(effect.FetchOffset, effect.MaxBytes)
	readResult := r.submitLoopCommand(ctx, machineReadLogResultCommand{
		Effect:  effect,
		Records: records,
		Err:     readErr,
	})
	if readResult.Err != nil {
		return channel.ReplicaFetchResult{}, readResult.Err
	}
	if readResult.Fetch == nil {
		return channel.ReplicaFetchResult{}, channel.ErrCorruptState
	}
	return readResult.Fetch.Result, nil
}

func (r *replica) applyReadLogResultCommand(cmd machineReadLogResultCommand) machineResult {
	effect := cmd.Effect

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return machineResult{Err: channel.ErrNotLeader}
	}
	if r.state.Role == channel.ReplicaRoleTombstoned {
		return machineResult{Err: channel.ErrTombstoned}
	}
	if effect.ChannelKey != r.state.ChannelKey || effect.Epoch != r.state.Epoch ||
		effect.RoleGeneration != r.roleGeneration {
		if r.state.Role != channel.ReplicaRoleLeader && r.state.Role != channel.ReplicaRoleFencedLeader {
			return machineResult{Err: channel.ErrNotLeader}
		}
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if r.state.Role != channel.ReplicaRoleLeader && r.state.Role != channel.ReplicaRoleFencedLeader {
		return machineResult{Err: channel.ErrNotLeader}
	}
	if effect.FetchOffset < r.state.LogStartOffset {
		return machineResult{Err: channel.ErrSnapshotRequired}
	}
	if r.state.LEO < effect.LeaderLEO {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if cmd.Err != nil {
		return machineResult{Err: cmd.Err}
	}
	if effect.EffectID == 0 || effect.MaxBytes <= 0 || effect.FetchOffset > effect.LeaderLEO {
		return machineResult{Err: channel.ErrCorruptState}
	}

	records := cloneRecords(cmd.Records)
	maxVisibleRecords := effect.LeaderLEO - effect.FetchOffset
	if uint64(len(records)) > maxVisibleRecords {
		records = records[:int(maxVisibleRecords)]
	}
	result := effect.Result
	result.Records = records
	return machineResult{Fetch: &machineFetchProgressResult{
		Result:      result,
		LeaderLEO:   effect.LeaderLEO,
		ChannelKey:  effect.ChannelKey,
		FetchOffset: effect.FetchOffset,
	}}
}
