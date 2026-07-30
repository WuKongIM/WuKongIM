package cluster

import (
	"context"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
)

type recordingProposer struct {
	calls    int
	last     propose.Request
	requests []propose.Request
	ctx      context.Context
}

func (p *recordingProposer) Propose(ctx context.Context, req propose.Request) error {
	p.calls++
	p.ctx = ctx
	p.last = req
	p.requests = append(p.requests, req)
	return nil
}

type noopChannelService struct{}

func (noopChannelService) Append(context.Context, channelruntime.AppendRequest) (channelruntime.AppendResult, error) {
	return channelruntime.AppendResult{}, nil
}

func (noopChannelService) AppendBatch(context.Context, channelruntime.AppendBatchRequest) (channelruntime.AppendBatchResult, error) {
	return channelruntime.AppendBatchResult{}, nil
}

func (noopChannelService) ResolveAppendAuthority(context.Context, channelruntime.ChannelID) (channelruntime.Meta, error) {
	return channelruntime.Meta{}, nil
}

func (noopChannelService) ReadChannelLastVisible(context.Context, channelruntime.ChannelID, uint64) (channelruntime.Message, bool, error) {
	return channelruntime.Message{}, false, nil
}

func (noopChannelService) RetentionView(context.Context, channelruntime.ChannelID) (channelruntime.RetentionView, error) {
	return channelruntime.RetentionView{}, nil
}

func (noopChannelService) ApplyRetentionBoundary(context.Context, channelruntime.RetentionApplyRequest) (channelruntime.RetentionApplyResult, error) {
	return channelruntime.RetentionApplyResult{}, nil
}

func (noopChannelService) RuntimeSnapshot(context.Context) (channelruntime.RuntimeSnapshot, error) {
	return channelruntime.RuntimeSnapshot{}, nil
}

func (noopChannelService) RuntimeProbe(context.Context, channelruntime.RuntimeSelector) (channelruntime.RuntimeProbeResult, error) {
	return channelruntime.RuntimeProbeResult{}, nil
}

func (noopChannelService) RuntimeEvict(context.Context, channelruntime.RuntimeSelector) (channelruntime.RuntimeEvictResult, error) {
	return channelruntime.RuntimeEvictResult{}, nil
}

func (noopChannelService) DrainChannel(context.Context, channelruntime.DrainChannelRequest) (channelruntime.DrainChannelResult, error) {
	return channelruntime.DrainChannelResult{}, nil
}

func (noopChannelService) Tick(context.Context) error { return nil }

func (noopChannelService) Close() error { return nil }
