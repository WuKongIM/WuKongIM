package transport

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	wktransport "github.com/WuKongIM/WuKongIM/pkg/transport"
)

// ProbeClientOptions configures the synchronous reconcile proof client.
type ProbeClientOptions struct {
	// Client is the shared node-to-node transport client.
	Client *wktransport.Client
	// RPCTimeout bounds requests when ctx does not already have a deadline.
	RPCTimeout time.Duration
}

// ProbeClient performs direct reconcile proof RPCs outside runtime sessions.
type ProbeClient struct {
	client     *wktransport.Client
	rpcTimeout time.Duration
}

// NewProbeClient builds a synchronous reconcile proof client.
func NewProbeClient(opts ProbeClientOptions) (*ProbeClient, error) {
	if opts.Client == nil {
		return nil, fmt.Errorf("channeltransport: probe client requires a transport client")
	}
	if opts.RPCTimeout <= 0 {
		opts.RPCTimeout = defaultRPCTimeout
	}
	return &ProbeClient{
		client:     opts.Client,
		rpcTimeout: opts.RPCTimeout,
	}, nil
}

// Probe fetches one reconcile proof from the target replica.
func (c *ProbeClient) Probe(ctx context.Context, peer channel.NodeID, req runtime.ReconcileProbeRequestEnvelope) (runtime.ReconcileProbeResponseEnvelope, error) {
	if c == nil || c.client == nil {
		return runtime.ReconcileProbeResponseEnvelope{}, fmt.Errorf("channeltransport: probe client not configured")
	}
	if peer == 0 {
		return runtime.ReconcileProbeResponseEnvelope{}, channel.ErrInvalidArgument
	}
	body, err := encodeReconcileProbeRequest(req)
	if err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	rpcCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && c.rpcTimeout > 0 {
		rpcCtx, cancel = context.WithTimeout(ctx, c.rpcTimeout)
	}
	defer cancel()

	respBody, err := c.client.RPCService(rpcCtx, uint64(peer), fetchRPCShardKey(req.ChannelKey), RPCServiceReconcileProbe, body)
	if err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	return decodeReconcileProbeResponse(respBody)
}
