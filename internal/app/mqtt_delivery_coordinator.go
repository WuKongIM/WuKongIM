package app

import (
	"context"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// mqttConnectionDeliveries transfers a newly opened stream to the shared
// scheduler. It retains no second task map; even a late context failure leaves
// an accepted task runtime-owned so entry closure can wake terminal cleanup.
type mqttConnectionDeliveries struct {
	coordinator *sessioncase.DeliveryCoordinator
	scheduler   *runtime.Deliveries
}

var _ access.ConnectionDeliveries = mqttConnectionDeliveries{}

func (d mqttConnectionDeliveries) Register(ctx context.Context, connection sessioncase.Connection, sink sessioncase.DeliverySink) error {
	if ctx == nil || d.coordinator == nil || d.scheduler == nil {
		return sessioncase.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	task, err := d.coordinator.Open(ctx, connection, sink)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = d.scheduler.Register(connection.Owner, task); err != nil {
		return err
	}
	return ctx.Err()
}

func (d mqttConnectionDeliveries) Wake(owner contract.Owner) error {
	return d.scheduler.Wake(owner)
}

// newMQTTDeliveryCoordinator binds discovery, accounting and sending to the same
// foreground Node and receive policy. App owns accepted-connection registration
// and must join its delivery runtime before closing these dependencies.
func newMQTTDeliveryCoordinator(node *cluster.Node, owners *runtime.Owners, authorization sessioncase.SubscriptionAuthorizer, sessions *sessioncase.App, maxSubscriptions int) (*sessioncase.DeliveryCoordinator, error) {
	sender, err := newMQTTSender(node, owners, authorization, sessions)
	if err != nil {
		return nil, err
	}
	accounting, err := newMQTTAccounting(node, authorization)
	if err != nil {
		return nil, err
	}
	return sessioncase.NewDeliveryCoordinator(sessioncase.DeliveryCoordinatorOptions{Sender: sender, Accounting: accounting, MaxSubscriptions: maxSubscriptions, IdleRefresh: 10 * time.Second})
}
