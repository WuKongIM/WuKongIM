package app

import (
	"context"
	"sync"
	"sync/atomic"

	accessgateway "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
)

// ClusterRuntime is the cluster lifecycle surface used by the app root.
type ClusterRuntime interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// GatewayRuntime is the gateway lifecycle surface used by the app root.
type GatewayRuntime interface {
	Start() error
	Stop() error
}

// Option customizes App construction.
type Option func(*App)

// App is the internalv2 composition root for cluster, message, and gateway runtimes.
type App struct {
	cfg      Config
	cluster  ClusterRuntime
	gateway  GatewayRuntime
	handler  *accessgateway.Handler
	messages *message.App

	lifecycleMu    sync.Mutex
	started        bool
	stopped        bool
	clusterStarted bool
	gatewayStarted bool
}

// New creates an internalv2 App.
func New(cfg Config, opts ...Option) (*App, error) {
	app := &App{cfg: cfg}
	clusterCfg := defaultClusterConfig(cfg)
	for _, opt := range opts {
		if opt != nil {
			opt(app)
		}
	}
	if app.cluster == nil {
		node, err := clusterv2.New(clusterCfg)
		if err != nil {
			return nil, err
		}
		app.cluster = node
		app.messages = message.New(message.Options{
			Appender:  clusterinfra.NewChannelAppender(node),
			MessageID: newNodeMessageIDs(clusterCfg.NodeID),
		})
	}
	if app.messages == nil {
		messageOpts := message.Options{MessageID: newNodeMessageIDs(clusterCfg.NodeID)}
		if appendNode, ok := app.cluster.(clusterinfra.ChannelAppendNode); ok {
			messageOpts.Appender = clusterinfra.NewChannelAppender(appendNode)
		}
		app.messages = message.New(messageOpts)
	}
	if app.handler == nil {
		app.handler = accessgateway.New(accessgateway.Options{Messages: app.messages, SendTimeout: cfg.Gateway.SendTimeout})
	}
	if app.gateway == nil && len(cfg.Gateway.Listeners) > 0 {
		gw, err := gateway.New(gateway.Options{
			Handler:        app.handler,
			Listeners:      cfg.Gateway.Listeners,
			DefaultSession: cfg.Gateway.Session,
			Transport:      cfg.Gateway.Transport,
		})
		if err != nil {
			return nil, err
		}
		app.gateway = gw
	}
	return app, nil
}

// WithCluster overrides the cluster runtime.
func WithCluster(cluster ClusterRuntime) Option {
	return func(a *App) { a.cluster = cluster }
}

// WithGateway overrides the gateway runtime.
func WithGateway(gateway GatewayRuntime) Option {
	return func(a *App) { a.gateway = gateway }
}

// WithMessages overrides the message usecase app.
func WithMessages(messages *message.App) Option {
	return func(a *App) { a.messages = messages }
}

// Handler returns the gateway access handler.
func (a *App) Handler() *accessgateway.Handler {
	if a == nil {
		return nil
	}
	return a.handler
}

// Messages returns the message usecase app.
func (a *App) Messages() *message.App {
	if a == nil {
		return nil
	}
	return a.messages
}

func defaultClusterConfig(cfg Config) clusterv2.Config {
	cluster := cfg.Cluster
	if cluster.NodeID == 0 {
		cluster.NodeID = cfg.NodeID
	}
	if cluster.DataDir == "" {
		cluster.DataDir = cfg.DataDir
	}
	return cluster
}

type nodeMessageIDs struct {
	next atomic.Uint64
}

func newNodeMessageIDs(nodeID uint64) *nodeMessageIDs {
	g := &nodeMessageIDs{}
	g.next.Store(nodeID << 48)
	return g
}

func (g *nodeMessageIDs) Next() uint64 {
	return g.next.Add(1)
}
