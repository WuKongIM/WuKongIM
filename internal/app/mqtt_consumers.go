package app

import (
	"context"

	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// wireMQTTConsumers joins existing policy ports; runtime owns only discovery,
// bounds and scheduling, while metrics retain no Session/source identities.
func (a *App) wireMQTTConsumers(node *cluster.Node, auth sessioncase.SubscriptionAuthorizer, ender sessioncase.SessionEnder) (*runtime.ConsumerWorker, error) {
	accounting, err := newMQTTAccounting(node, auth)
	if err != nil {
		return nil, err
	}
	progress, err := newMQTTSourceProgress(node)
	if err != nil {
		return nil, err
	}
	removal, err := newMQTTSourceRemoval(node)
	if err != nil {
		return nil, err
	}
	maintenance, err := sessioncase.NewConsumerMaintenance(sessioncase.ConsumerMaintenanceOptions{Store: node, Accounting: accounting, Progress: progress, Removal: removal, Ender: ender})
	if err != nil {
		return nil, err
	}
	opts := runtime.ConsumerWorkerOptions{Source: node, Maintainer: mqttConsumerMaintenance{maintenance}, Registry: a.goroutines, Workers: a.cfg.MQTT.Workers, HashSlotCount: defaultClusterConfig(a.cfg).Slots.HashSlotCount}
	if a.metrics != nil {
		opts.Observe = func(o runtime.ConsumerObservation) {
			m := a.metrics.MQTT
			for _, e := range []struct {
				name  string
				count int
			}{{"pages", o.Pages}, {"visited", o.Visited}, {"scheduled", o.Scheduled}, {"completed", o.Completed}, {"failures", o.Failures}, {"accounted", o.Accounted}, {"projected", o.Projected}, {"removed", o.Removed}, {"quota_end_confirmed", o.QuotaEnded}, {"revocation_end_confirmed", o.RevokedEnded}} {
				m.ObserveConsumer(e.name, uint64(e.count))
			}
			m.SetConsumerWork(o.Admitted, o.Capacity)
		}
	}
	return runtime.NewConsumerWorker(opts)
}

type mqttConsumerMaintenance struct {
	maintenance *sessioncase.ConsumerMaintenance
}

func (m mqttConsumerMaintenance) MaintainConsumer(ctx context.Context, k meta.MQTTSourceBindingKey) (runtime.ConsumerWork, error) {
	r, err := m.maintenance.Maintain(ctx, k)
	return runtime.ConsumerWork{Accounted: r.Accounted, Projected: r.Projected, Removed: r.Removed, QuotaEnded: r.QuotaEnded, RevokedEnded: r.RevokedEnded}, err
}
