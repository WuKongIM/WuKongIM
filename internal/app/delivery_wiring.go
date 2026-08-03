package app

import (
	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	deliveryinfra "github.com/WuKongIM/WuKongIM/internal/infra/delivery"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
)

func (a *App) wireDelivery() {
	if !a.cfg.Delivery.Enabled || a.onlineDelivery != nil {
		return
	}
	localNodeID := a.cfg.Cluster.NodeID
	if localNodeID == 0 {
		localNodeID = a.cfg.NodeID
	}
	var remote runtimedelivery.RemoteOwnerPusher
	if presenceNode, ok := a.cluster.(clusterinfra.PresenceNode); ok {
		localNodeID = presenceNode.NodeID()
		remote = accessnode.NewClient(presenceNode)
	}
	offlineBatch := composeOfflineRecipientObservers(a.pluginReceive, a.webhookOffline)
	var offlineObserver runtimedelivery.OfflineRecipientsObserver
	if offlineBatch != nil {
		offlineObserver = onlineDeliveryOfflineObserver{next: offlineBatch}
	}
	observer := a.onlineDeliveryObserver()
	runtime := runtimedelivery.NewRuntime(runtimedelivery.RuntimeOptions{
		LocalNodeID:               localNodeID,
		Presence:                  deliveryinfra.NewPresenceResolver(a.presence),
		RemoteOwnerPusher:         remote,
		SessionWriter:             deliveryinfra.NewLocalSessionWriter(deliveryinfra.LocalSessionWriterOptions{Online: a.online, Logger: a.logger.Named("delivery.owner")}),
		OfflineRecipientsObserver: offlineObserver,
		QueueSize:                 a.cfg.Delivery.EventQueueSize,
		Workers:                   a.cfg.Delivery.RecipientWorkerConcurrency,
		MaxPlanRecipients:         a.cfg.Delivery.PushBatchSize,
		OwnerPushBatchSize:        a.cfg.Delivery.PushBatchSize,
		RetryMaxAttempts:          defaultDeliveryRetryMaxAttempts,
		RetryInitialBackoff:       defaultDeliveryRetryBackoff,
		RetryMaxBackoff:           defaultDeliveryRetryBackoff,
		PendingAckTTL:             a.cfg.Delivery.PendingAckTTL,
		Observer:                  observer,
		AckObserver:               observer,
		AckBatchObserver:          observer,
		Goroutines:                a.goroutines,
		Acks: runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{
			MaxPendingPerSession: a.cfg.Delivery.PendingAckMaxPerSession,
		}),
	})
	a.onlineDelivery = runtime
	a.delivery = deliveryusecase.New(deliveryusecase.Options{Runtime: onlineDeliveryUsecaseAdapter{runtime: runtime}})
	a.deliveryWorker = runtime
	if presenceNode, ok := a.cluster.(clusterinfra.PresenceNode); ok {
		adapter := accessnode.New(accessnode.Options{
			Delivery: accessnode.AdaptOnlineDeliveryOwnerPush(runtime),
			Logger:   a.logger.Named("node"),
		})
		presenceNode.RegisterRPC(accessnode.DeliveryPushRPCServiceID, nodeRPCHandlerFunc(adapter.HandleDeliveryPushRPC))
	}
}
