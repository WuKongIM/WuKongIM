package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/infra/mqttowner"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
)

// mqttProduct owns only composition and the dependency-safe lifecycle of the
// MQTT entry. Session policy and scheduling remain in their respective modules.
type mqttProduct struct {
	handler     *access.Handler
	owners      *runtime.Owners
	sweeper     *runtime.OwnerSweeper
	connections *runtime.Connections
	deliveries  *runtime.Deliveries
	deadlines   *runtime.DeadlineWorker
	replay      *runtime.ReplayWorker
	wills       *runtime.WillWorker
	retirements *mqttowner.Retirements
	consumers   *runtime.ConsumerWorker
}

func (a *App) wireMQTT(nodeID uint64) error {
	if !a.cfg.MQTT.Enabled {
		return nil
	}
	node, ok := a.cluster.(*cluster.Node)
	if !ok || node == nil || a.users == nil || a.messages == nil || a.gateway != nil {
		return fmt.Errorf("%w: MQTT requires the owned cluster, users, messages and gateway", ErrInvalidConfig)
	}
	c := a.cfg.MQTT
	var boot [16]byte
	if _, err := rand.Read(boot[:]); err != nil {
		return err
	}
	owners, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: nodeID, BootID: hex.EncodeToString(boot[:]), Capacity: c.MaxConnections, MaxOperations: 8, PendingTimeout: 5 * time.Second, MaxLease: time.Minute, CloseRetry: 250 * time.Millisecond})
	if err != nil {
		return err
	}
	m := &mqttProduct{owners: owners}
	a.mqtt = m // Constructor failure must retain ownership for App.Stop cleanup.
	m.sweeper, err = a.wireMQTTOwnerSweeper(owners)
	if err != nil {
		return err
	}
	m.retirements, err = mqttowner.NewRetirements(filepath.Join(defaultClusterConfig(a.cfg).DataDir, "mqtt", "retired-owners"), nodeID)
	if err != nil {
		return err
	}
	node.RegisterRPC(accessnode.MQTTOwnerRPCServiceID, accessnode.MQTTOwnerRPC{Owners: runtime.Isolation{Owners: owners, Retired: m.retirements}})
	sessions, err := sessioncase.New(sessioncase.Options{Store: node, Owners: owners, Isolation: accessnode.NewMQTTOwnerClient(node), Tokens: a.users, Wills: mqttWillAuthorizer{messages: a.messages}, LeaseDuration: 30 * time.Second, CleanupTimeout: time.Second, SessionExpiryLimitSec: c.SessionExpiryLimitSec, QuotaMessages: c.QuotaMessages, QuotaBytes: c.QuotaBytes, WindowLimit: c.WindowLimit})
	if err != nil {
		return err
	}
	m.connections, err = runtime.NewConnections(runtime.ConnectionOptions{Owners: owners, Control: mqttConnectionControl{sessions: sessions}, Registry: a.goroutines, Workers: c.Workers})
	if err != nil {
		return err
	}
	m.deliveries, err = runtime.NewDeliveries(runtime.DeliveryOptions{Owners: owners, Registry: a.goroutines, Workers: c.Workers})
	if err != nil {
		return err
	}
	authorization, err := newMQTTReceiveAuthorization(node)
	if err != nil {
		return err
	}
	m.consumers, err = a.wireMQTTConsumers(node, authorization, sessions)
	if err != nil {
		return err
	}
	groups, err := newMQTTGroupProjection(node, owners, authorization, a.messageIDs)
	if err != nil {
		return err
	}
	inbox, err := newMQTTInboxProjection(node, owners, authorization, a.messageIDs)
	if err != nil {
		return err
	}
	subscriptions, err := sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: node, Owners: owners, Authorization: authorization, Projection: mqttProductProjection{groups: groups, inbox: inbox}, MaxSubscriptions: c.MaxSubscriptions})
	if err != nil {
		return err
	}
	requests, err := sessioncase.NewSubscriptionRequests(sessioncase.SubscriptionRequestOptions{Subscriptions: subscriptions})
	if err != nil {
		return err
	}
	coordinator, err := newMQTTDeliveryCoordinator(node, owners, authorization, sessions, c.MaxSubscriptions)
	if err != nil {
		return err
	}
	acks, err := newMQTTAcknowledgements(node, owners)
	if err != nil {
		return err
	}
	publisher, err := access.NewPublisher(access.PublisherOptions{Owners: owners, Messages: a.messages})
	if err != nil {
		return err
	}
	m.handler, err = access.NewHandler(access.HandlerOptions{Namespace: c.Namespace, Sessions: sessions, Connections: m.connections, Owners: owners, Publisher: publisher, Acknowledgements: acks, Deliveries: mqttConnectionDeliveries{coordinator: coordinator, scheduler: m.deliveries}, Subscriptions: requests, MaxPacketBytes: c.MaxPacketBytes})
	if err != nil {
		return err
	}
	hashSlots := defaultClusterConfig(a.cfg).Slots.HashSlotCount
	m.replay, err = newMQTTReplayWorker(node, a.messageIDs, runtime.ReplayWorkerOptions{Registry: a.goroutines, HashSlotCount: hashSlots})
	if err != nil {
		return err
	}
	m.deadlines, err = runtime.NewDeadlineWorker(runtime.DeadlineWorkerOptions{Source: node, Reconciler: sessions, Registry: a.goroutines, HashSlotCount: hashSlots})
	if err != nil {
		return err
	}
	executor, err := newMQTTWillExecutor(node, a.messages, sessioncase.WillExecutionOptions{NodeID: nodeID, BootID: hex.EncodeToString(boot[:]), LeaseDuration: 10 * time.Second, TurnTimeout: 5 * time.Second})
	if err != nil {
		return err
	}
	m.wills, err = runtime.NewWillWorker(runtime.WillWorkerOptions{Source: node, Executor: mqttWillExecution{executor}, Registry: a.goroutines, HashSlotCount: hashSlots})
	if err != nil {
		return err
	}
	a.cfg.Gateway.Listeners = append(a.cfg.Gateway.Listeners, gateway.ListenerOptions{Name: "mqtt", Network: "tcp", Transport: "gnet", Protocol: "mqtt", Address: c.ListenAddr})
	return nil
}

// Start precedes gateway admission and follows readiness of every dependency.
func (m *mqttProduct) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	for _, w := range []WorkerRuntime{m.sweeper, m.replay, m.deadlines, m.wills, m.consumers, m.connections, m.deliveries} {
		if err := w.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Stop is retryable after a timeout. Callers must retain cluster and message
// dependencies until it succeeds; fencing alone never proves owner isolation.
func (m *mqttProduct) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.owners.StopAdmission()
	var result error
	if m.sweeper != nil {
		result = errors.Join(result, m.sweeper.Stop(ctx))
	}
	if m.deliveries != nil {
		result = errors.Join(result, m.deliveries.Stop(ctx))
	}
	if m.consumers != nil {
		result = errors.Join(result, m.consumers.Stop(ctx))
	}
	if m.wills != nil {
		result = errors.Join(result, m.wills.Stop(ctx))
	}
	if m.deadlines != nil {
		result = errors.Join(result, m.deadlines.Stop(ctx))
	}
	if m.replay != nil {
		result = errors.Join(result, m.replay.Stop(ctx))
	}
	if m.connections != nil {
		result = errors.Join(result, m.connections.Stop(ctx))
	}
	result = errors.Join(result, m.owners.Close(ctx))
	if result != nil || m.retirements == nil {
		return result
	}
	// Persist only after every producer and owner has joined. A later process
	// may reuse this proof without guessing from the old Session state or lease.
	proof, err := m.owners.Retirement()
	if err != nil {
		return err
	}
	return m.retirements.Record(ctx, proof)
}

// mqttProductProjection selects a composed port; each port owns validation and
// durable establishment/removal policy for its target.
type mqttProductProjection struct {
	groups, inbox sessioncase.SubscriptionProjection
}

func (p mqttProductProjection) target(r sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjection, error) {
	switch r.Subscription.TargetKind {
	case meta.MQTTSubscriptionGroup:
		return p.groups, nil
	case meta.MQTTSubscriptionUserInbox:
		return p.inbox, nil
	default:
		return nil, sessioncase.ErrInvalid
	}
}
func (p mqttProductProjection) Establish(ctx context.Context, r sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
	target, err := p.target(r)
	if err != nil {
		return sessioncase.SubscriptionProjectionReceipt{}, err
	}
	return target.Establish(ctx, r)
}
func (p mqttProductProjection) Remove(ctx context.Context, r sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
	target, err := p.target(r)
	if err != nil {
		return sessioncase.SubscriptionProjectionReceipt{}, err
	}
	return target.Remove(ctx, r)
}

// mqttWillExecution maps the usecase result without turning pending work into
// a retry decision. Only the authoritative recovery index schedules later turns.
type mqttWillExecution struct{ executor *sessioncase.WillExecutor }

func (e mqttWillExecution) ExecuteWill(ctx context.Context, key meta.MQTTWillKey) error {
	result, err := e.executor.Execute(ctx, key)
	if err != nil {
		return err
	}
	if result.Pending {
		return sessioncase.ErrWillPending
	}
	return nil
}
