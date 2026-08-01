package app

import (
	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	deliveryinfra "github.com/WuKongIM/WuKongIM/internal/infra/delivery"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
)

func (a *App) wireDelivery() {
	if a.cfg.Delivery.Enabled && a.delivery == nil {
		localPusher := deliveryinfra.NewLocalOwnerPusher(deliveryinfra.LocalOwnerPusherOptions{
			Online:        a.online,
			PendingAckTTL: a.cfg.Delivery.PendingAckTTL,
			Logger:        a.logger.Named("delivery.owner"),
		})
		a.localOwnerPusher = localPusher
		deliveryObserver := a.deliveryObserver()
		var push runtimedelivery.Pusher = localPusher
		var fanoutRemote runtimedelivery.FanoutTaskForwarder
		var localNodeID uint64
		if presenceNode, ok := a.cluster.(clusterinfra.PresenceNode); ok {
			localNodeID = presenceNode.NodeID()
			nodeClient := accessnode.NewClient(presenceNode)
			push = clusterinfra.NewDeliveryPusher(localNodeID, localPusher, nodeClient)
			fanoutRemote = nodeClient
		}
		var partitioner runtimedelivery.Partitioner
		if routes, ok := a.cluster.(clusterWriteReadyRuntime); ok {
			partitioner = clusterinfra.NewDeliveryPartitioner(routes)
		}
		fanoutWorker := runtimedelivery.NewFanoutWorker(runtimedelivery.FanoutWorkerOptions{
			Subscribers: appSubscriberPlanner{
				channel: runtimedelivery.NewChannelSubscriberPlanner(runtimedelivery.ChannelSubscriberPlannerOptions{
					Source: a.deliverySubscribers,
				}),
			},
			Presence:      presenceResolverAdapter{presence: a.presence},
			Push:          push,
			PageSize:      a.cfg.Delivery.FanoutPageSize,
			PushBatchSize: a.cfg.Delivery.PushBatchSize,
			Observer:      deliveryObserver,
		})
		var fanoutRunner runtimedelivery.FanoutTaskRunner = fanoutWorker
		if localNodeID != 0 {
			fanoutRunner = runtimedelivery.NewFanoutTaskRouter(runtimedelivery.FanoutTaskRouterOptions{
				LocalNodeID: localNodeID,
				Local:       fanoutWorker,
				Remote:      fanoutRemote,
				Observer:    deliveryObserver,
			})
		}
		var retryObserver runtimedelivery.RetryObserver
		if observer, ok := deliveryObserver.(runtimedelivery.RetryObserver); ok {
			retryObserver = observer
		}
		retryScheduler := runtimedelivery.NewRetryScheduler(runtimedelivery.RetrySchedulerOptions{
			Runner:      fanoutRunner,
			Capacity:    a.cfg.Delivery.EventQueueSize,
			MaxAttempts: defaultDeliveryRetryMaxAttempts,
			Backoff:     defaultDeliveryRetryBackoff,
			Observer:    retryObserver,
			Goroutines:  a.goroutines,
		})
		var managerObserver runtimedelivery.ManagerObserver
		if observer, ok := deliveryObserver.(runtimedelivery.ManagerObserver); ok {
			managerObserver = observer
		}
		var ackObserver runtimedelivery.AckObserver
		if observer, ok := deliveryObserver.(runtimedelivery.AckObserver); ok {
			ackObserver = observer
		}
		var ackBatchObserver runtimedelivery.AckBatchObserver
		if a.metrics != nil {
			ackBatchObserver = deliveryMetricsObserver{metrics: a.metrics}
		}
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
			Planner:          runtimedelivery.NewPlanner(runtimedelivery.PlannerOptions{Partitioner: partitioner}),
			Runner:           retryScheduler,
			AsyncQueueSize:   a.cfg.Delivery.EventQueueSize,
			AsyncWorkers:     1,
			ManagerObserver:  managerObserver,
			Goroutines:       a.goroutines,
			AckObserver:      ackObserver,
			AckBatchObserver: ackBatchObserver,
			Acks: runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{
				MaxPendingPerSession: a.cfg.Delivery.PendingAckMaxPerSession,
			}),
		})
		localPusher.SetAckManager(manager)
		a.deliveryManager = manager
		a.deliveryRetry = retryScheduler
		a.delivery = deliveryusecase.New(deliveryusecase.Options{Runtime: deliveryRuntimeAdapter{manager: manager}})
		if a.deliveryWorker == nil {
			a.deliveryWorker = deliveryWorkerGroup{retryScheduler, manager}
		}
		if presenceNode, ok := a.cluster.(clusterinfra.PresenceNode); ok {
			adapter := accessnode.New(accessnode.Options{Delivery: localPusher, DeliveryFanout: fanoutWorker, Logger: a.logger.Named("node")})
			presenceNode.RegisterRPC(accessnode.DeliveryPushRPCServiceID, nodeRPCHandlerFunc(adapter.HandleDeliveryPushRPC))
			presenceNode.RegisterRPC(accessnode.DeliveryFanoutRPCServiceID, nodeRPCHandlerFunc(adapter.HandleDeliveryFanoutRPC))
		}
	}
}
