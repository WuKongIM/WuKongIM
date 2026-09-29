package app

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
)

// wireChannelSubmissions derives one bounded gateway append owner from existing
// dispatch settings. It neither creates another ingress allowance nor changes
// the shared Router's cluster routing and durability semantics.
func (a *App) wireChannelSubmissions(router *channelappend.Router) error {
	runtime := gateway.NormalizeRuntimeOptions(a.cfg.Gateway.Runtime)
	session := gateway.NormalizeSessionOptions(a.cfg.Gateway.Session)
	workers := min(runtime.AsyncSendWorkers, runtime.AsyncSendQueueCapacity)
	// Keep at most one maximum micro-batch worth of payload per execution worker,
	// while still permitting one legal inbound frame larger than the batch target.
	maxInt := int(^uint(0) >> 1)
	if workers <= 0 || session.AsyncSendBatchMaxBytes > maxInt/workers {
		return fmt.Errorf("%w: gateway submission payload budget overflow", ErrInvalidConfig)
	}
	payloadCapacity := max(workers*session.AsyncSendBatchMaxBytes, session.MaxInboundBytes)
	owner, err := channelappend.NewOrderedSubmitter(channelappend.OrderedSubmitterOptions{Workers: workers, Capacity: runtime.AsyncSendQueueCapacity, PayloadCapacity: payloadCapacity, BatchMaxRecords: session.AsyncSendBatchMaxRecords, BatchMaxBytes: session.AsyncSendBatchMaxBytes, CommandChannelSuffix: a.cfg.Message.CMDChannelSuffix, Goroutines: a.goroutines}, router)
	if err != nil {
		return fmt.Errorf("internal/app: create gateway channel submission owner: %w", err)
	}
	a.channelSubmissions = owner
	return nil
}

func (a *App) gatewayHandler() gateway.Handler {
	if a.handler == nil {
		return nil
	}
	return a.handler.WithDeferredSends(a.deferredGatewayMessages)
}

// drainGatewaySubmissions preserves the gateway's one-way fence on timeout;
// dependency shutdown may proceed only once every publication has joined.
func (a *App) drainGatewaySubmissions(ctx context.Context) error {
	if a.channelSubmissions == nil {
		return nil
	}
	if drainer, ok := a.gateway.(interface{ DrainSends(context.Context) error }); ok {
		return drainer.DrainSends(ctx)
	}
	return nil
}

func (a *App) closeChannelSubmissions(ctx context.Context) error {
	if a.channelSubmissions == nil {
		return nil
	}
	return a.channelSubmissions.Close(ctx)
}
