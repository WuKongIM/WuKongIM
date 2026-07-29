package channels

import (
	"context"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
)

// ServiceGateway keeps transport handlers stable while restore activation
// replaces the underlying Channel runtime and message-store factory.
type ServiceGateway struct {
	current atomic.Pointer[Service]
}

// NewServiceGateway creates a stable gateway for one live Channel service.
func NewServiceGateway(service *Service) *ServiceGateway {
	gateway := &ServiceGateway{}
	gateway.Replace(service)
	return gateway
}

// Replace publishes a fully constructed Channel service for subsequent RPCs.
// Controller maintenance fences ordinary traffic while restore uses this swap.
func (g *ServiceGateway) Replace(service *Service) {
	if g == nil || service == nil {
		return
	}
	g.current.Store(service)
}

// Server exposes the gateway itself as the stable replication endpoint.
func (g *ServiceGateway) Server() channeltransport.Server { return g }

func (g *ServiceGateway) service() (*Service, error) {
	if g == nil {
		return nil, ch.ErrNotReady
	}
	service := g.current.Load()
	if service == nil {
		return nil, ch.ErrNotReady
	}
	return service, nil
}

// Append forwards one remote append to the currently published service.
func (g *ServiceGateway) Append(
	ctx context.Context,
	request ch.AppendRequest,
) (ch.AppendResult, error) {
	service, err := g.service()
	if err != nil {
		return ch.AppendResult{}, err
	}
	return service.Append(ctx, request)
}

// AppendBatch forwards one remote append batch to the current service.
func (g *ServiceGateway) AppendBatch(
	ctx context.Context,
	request ch.AppendBatchRequest,
) (ch.AppendBatchResult, error) {
	service, err := g.service()
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	return service.AppendBatch(ctx, request)
}

func (g *ServiceGateway) observeAppendStage(
	stage string,
	err error,
	duration time.Duration,
) {
	service, loadErr := g.service()
	if loadErr == nil {
		service.observeAppendStage(stage, err, duration)
	}
}

func (g *ServiceGateway) handleForwardLastVisible(
	ctx context.Context,
	request LastVisibleRequest,
) (LastVisibleResponse, error) {
	service, err := g.service()
	if err != nil {
		return LastVisibleResponse{}, err
	}
	return service.handleForwardLastVisible(ctx, request)
}

// HandlePull forwards replication pulls to the current service.
func (g *ServiceGateway) HandlePull(
	ctx context.Context,
	request channeltransport.PullRequest,
) (channeltransport.PullResponse, error) {
	service, err := g.service()
	if err != nil {
		return channeltransport.PullResponse{}, err
	}
	return service.Server().HandlePull(ctx, request)
}

// HandleAck forwards replication acknowledgements to the current service.
func (g *ServiceGateway) HandleAck(
	ctx context.Context,
	request channeltransport.AckRequest,
) error {
	service, err := g.service()
	if err != nil {
		return err
	}
	return service.Server().HandleAck(ctx, request)
}

// HandlePullHint forwards replication pull hints to the current service.
func (g *ServiceGateway) HandlePullHint(
	ctx context.Context,
	request channeltransport.PullHintRequest,
) error {
	service, err := g.service()
	if err != nil {
		return err
	}
	return service.Server().HandlePullHint(ctx, request)
}

// HandleNotify forwards legacy replication notifications to the current service.
func (g *ServiceGateway) HandleNotify(
	ctx context.Context,
	request channeltransport.NotifyRequest,
) error {
	service, err := g.service()
	if err != nil {
		return err
	}
	return service.Server().HandleNotify(ctx, request)
}

// HandlePullBatch preserves grouped replication pulls across runtime swaps.
func (g *ServiceGateway) HandlePullBatch(
	ctx context.Context,
	request channeltransport.PullBatchRequest,
) (channeltransport.PullBatchResponse, error) {
	service, err := g.service()
	if err != nil {
		return channeltransport.PullBatchResponse{}, err
	}
	return handlePullBatch(ctx, service.Server(), request)
}

// HandlePullHintBatch preserves grouped pull hints across runtime swaps.
func (g *ServiceGateway) HandlePullHintBatch(
	ctx context.Context,
	request channeltransport.PullHintBatchRequest,
) (channeltransport.PullHintBatchResponse, error) {
	service, err := g.service()
	if err != nil {
		return channeltransport.PullHintBatchResponse{}, err
	}
	return handlePullHintBatch(ctx, service.Server(), request)
}

var _ serviceRPCServer = (*ServiceGateway)(nil)
var _ channeltransport.BatchServer = (*ServiceGateway)(nil)
