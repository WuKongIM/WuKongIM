package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/infra/mqttowner"
	"github.com/WuKongIM/WuKongIM/internal/infra/mqttwill"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
)

// mqttGeneration owns one immutable composition with a fresh Owner boot.
// Terminal runtimes are never reopened after a restore or shutdown.
type mqttGeneration struct {
	// authMu guards CONNECT work that may precede an Owner reservation. Stop
	// cancels and joins it before retirement or storage replacement can proceed.
	authMu      sync.Mutex
	authContext context.Context
	cancelAuth  context.CancelFunc
	authCalls   int
	authDone    chan struct{}
	authStopped bool
	// restoring switches only runtime retirement; it never bypasses a business
	// mutation or turns a local close into a durable Session/Will receipt.
	restoring   atomic.Bool
	handler     *access.Handler
	owners      *runtime.Owners
	sweeper     *runtime.OwnerSweeper
	connections *runtime.Connections
	deliveries  *runtime.Deliveries
	deadlines   *runtime.DeadlineWorker
	replay      *runtime.ReplayWorker
	wills       *runtime.WillWorker
	retirements *mqttowner.Retirements
	// dispatches shares the exclusive MQTT generation lock and precedes Started.
	dispatches *mqttwill.Attempts
	consumers  *runtime.ConsumerWorker
}

func (a *App) wireMQTT(nodeID uint64) error {
	if !a.cfg.MQTT.Enabled {
		return nil
	}
	node, ok := a.cluster.(*cluster.Node)
	if !ok || node == nil || a.users == nil || a.messages == nil || a.gateway != nil {
		return fmt.Errorf("%w: MQTT requires the owned cluster, users, messages and gateway", ErrInvalidConfig)
	}
	m := &mqttProduct{build: func(ctx context.Context) (*mqttGeneration, error) { return a.newMQTTGeneration(ctx, node, nodeID) }}
	a.mqtt = m // Retain partial construction for dependency-safe cleanup.
	var err error
	m.pending, err = m.build(context.Background())
	if err != nil {
		return err
	}
	node.RegisterRPC(accessnode.MQTTOwnerRPCServiceID, accessnode.MQTTOwnerRPC{Owners: m})
	node.RegisterRPC(accessnode.MQTTWillRPCServiceID, accessnode.MQTTWillRPC{Attempts: m})
	a.cfg.Gateway.Listeners = append(a.cfg.Gateway.Listeners, gateway.ListenerOptions{Name: "mqtt", Network: "tcp", Transport: "gnet", Protocol: "mqtt", Address: a.cfg.MQTT.ListenAddr})
	return nil
}

// newMQTTGeneration constructs every terminal component afresh. A partial result
// stays owned by mqttProduct until its workers, Owners and file lock join.
func (a *App) newMQTTGeneration(ctx context.Context, node *cluster.Node, nodeID uint64) (m *mqttGeneration, err error) {
	c := a.cfg.MQTT
	var boot [16]byte
	if _, err := rand.Read(boot[:]); err != nil {
		return m, err
	}
	owners, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: nodeID, BootID: hex.EncodeToString(boot[:]), Capacity: c.MaxConnections, MaxOperations: 8, PendingTimeout: 5 * time.Second, MaxLease: time.Minute, CloseRetry: 250 * time.Millisecond})
	if err != nil {
		return m, err
	}
	m = &mqttGeneration{owners: owners}
	m.authContext, m.cancelAuth = context.WithCancel(context.Background())
	m.sweeper, err = a.wireMQTTOwnerSweeper(owners)
	if err != nil {
		return m, err
	}
	m.retirements, err = mqttowner.NewRetirements(filepath.Join(defaultClusterConfig(a.cfg).DataDir, "mqtt", "retired-owners"), nodeID)
	if err != nil {
		return m, err
	}
	// Prove earlier boots and mark this generation started before it admits
	// owners or workers. Stable entry/RPC dispatch still serves the retired run.
	recoverCtx, cancelRecover := context.WithTimeout(ctx, 10*time.Second)
	err = m.retirements.Recover(recoverCtx, hex.EncodeToString(boot[:]))
	cancelRecover()
	if err != nil {
		return m, fmt.Errorf("mqtt: recover owner retirements: %w", err)
	}
	m.dispatches, err = mqttwill.Open(filepath.Join(defaultClusterConfig(a.cfg).DataDir, "mqtt", "will-dispatches"), nodeID, hex.EncodeToString(boot[:]), m.retirements)
	if err != nil {
		return m, fmt.Errorf("mqtt: open Will dispatch journal: %w", err)
	}
	sessions, err := sessioncase.New(sessioncase.Options{Store: node, Owners: owners, Isolation: accessnode.NewMQTTOwnerClient(node), Tokens: a.users, Wills: mqttWillAuthorizer{messages: a.messages}, LeaseDuration: 30 * time.Second, CleanupTimeout: time.Second, SessionExpiryLimitSec: c.SessionExpiryLimitSec, QuotaMessages: c.QuotaMessages, QuotaBytes: c.QuotaBytes, WindowLimit: c.WindowLimit})
	if err != nil {
		return m, err
	}
	m.connections, err = runtime.NewConnections(runtime.ConnectionOptions{Owners: owners, Control: mqttConnectionControl{sessions: sessions, owners: owners, restoring: &m.restoring}, Registry: a.goroutines, Workers: c.Workers})
	if err != nil {
		return m, err
	}
	m.deliveries, err = runtime.NewDeliveries(runtime.DeliveryOptions{Owners: owners, Registry: a.goroutines, Workers: c.Workers, MaxSourcesPerTask: c.MaxSubscriptions})
	if err != nil {
		return m, err
	}
	authorization, err := newMQTTReceiveAuthorization(node)
	if err != nil {
		return m, err
	}
	groups, err := newMQTTGroupProjection(node, owners, authorization, a.messageIDs)
	if err != nil {
		return m, err
	}
	inbox, err := newMQTTInboxProjection(node, owners, authorization, a.messageIDs)
	if err != nil {
		return m, err
	}
	m.consumers, err = a.wireMQTTConsumers(node, authorization, sessions, groups, inbox)
	if err != nil {
		return m, err
	}
	subscriptions, err := sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: node, Owners: owners, Authorization: authorization, Projection: mqttProductProjection{groups: groups, inbox: inbox}, MaxSubscriptions: c.MaxSubscriptions})
	if err != nil {
		return m, err
	}
	requests, err := sessioncase.NewSubscriptionRequests(sessioncase.SubscriptionRequestOptions{Subscriptions: subscriptions})
	if err != nil {
		return m, err
	}
	coordinator, err := newMQTTDeliveryCoordinator(node, owners, authorization, sessions, c.MaxSubscriptions)
	if err != nil {
		return m, err
	}
	acks, err := newMQTTAcknowledgements(node, owners)
	if err != nil {
		return m, err
	}
	publisher, err := access.NewPublisher(access.PublisherOptions{Owners: owners, Messages: a.messages})
	if err != nil {
		return m, err
	}
	handlerOptions := access.HandlerOptions{Namespace: c.Namespace, Sessions: sessions, Connections: m.connections, Owners: owners, Publisher: publisher, Acknowledgements: acks, Deliveries: mqttConnectionDeliveries{coordinator: coordinator, scheduler: m.deliveries}, Subscriptions: requests, MaxPacketBytes: c.MaxPacketBytes}
	if a.metrics != nil {
		handlerOptions.ObserveSubscriptionClose = a.metrics.MQTT.ObserveSubscriptionClose
	}
	m.handler, err = access.NewHandler(handlerOptions)
	if err != nil {
		return m, err
	}
	hashSlots := defaultClusterConfig(a.cfg).Slots.HashSlotCount
	m.replay, err = newMQTTReplayWorker(node, a.messageIDs, runtime.ReplayWorkerOptions{Registry: a.goroutines, HashSlotCount: hashSlots}, func(source string) { _ = m.deliveries.WakeSource(source) })
	if err != nil {
		return m, err
	}
	m.deadlines, err = runtime.NewDeadlineWorker(runtime.DeadlineWorkerOptions{Source: node, Reconciler: sessions, Registry: a.goroutines, HashSlotCount: hashSlots})
	if err != nil {
		return m, err
	}
	executor, err := newMQTTWillExecutor(node, a.messages, sessioncase.WillExecutionOptions{DispatchFence: mqttWillDispatches{Attempts: m.dispatches, remote: accessnode.NewMQTTWillClient(node)}, ReclamationJournal: m.dispatches, NodeID: nodeID, BootID: hex.EncodeToString(boot[:]), LeaseDuration: 10 * time.Second, TurnTimeout: 5 * time.Second})
	if err != nil {
		return m, err
	}
	m.wills, err = runtime.NewWillWorker(runtime.WillWorkerOptions{Source: node, Executor: mqttWillExecution{executor}, Registry: a.goroutines, HashSlotCount: hashSlots})
	if err != nil {
		return m, err
	}
	return m, nil
}

// Start precedes gateway admission and follows readiness of every dependency.
func (m *mqttGeneration) Start(ctx context.Context) error {
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
func (m *mqttGeneration) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.owners.StopAdmission()
	if err := m.stopAuthentication(ctx); err != nil {
		return err
	}
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
	if m.dispatches != nil {
		m.dispatches.Close()
	}
	// Persist only after every producer and owner has joined. A later process
	// may reuse this proof without guessing from the old Session state or lease.
	proof, err := m.owners.Retirement()
	if err != nil {
		return err
	}
	if err = m.retirements.Record(ctx, proof); err != nil {
		return err
	}
	// Release the data-directory lock only after the graceful receipt is durable.
	return m.retirements.Close()
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
