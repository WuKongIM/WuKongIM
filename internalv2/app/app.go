package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	accessgateway "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
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

// APIRuntime is the HTTP API lifecycle surface used by the app root.
type APIRuntime interface {
	Start() error
	Stop(context.Context) error
}

// Option customizes App construction.
type Option func(*App)

// App is the internalv2 composition root for cluster, message, and gateway runtimes.
type App struct {
	cfg      Config
	cluster  ClusterRuntime
	api      APIRuntime
	gateway  GatewayRuntime
	handler  *accessgateway.Handler
	messages *message.App
	metrics  *obsmetrics.Registry

	lifecycleMu    sync.Mutex
	started        bool
	stopped        bool
	clusterStarted bool
	apiStarted     bool
	gatewayStarted bool
}

// New creates an internalv2 App.
func New(cfg Config, opts ...Option) (*App, error) {
	app := &App{cfg: cfg}
	clusterCfg := defaultClusterConfig(cfg)
	if cfg.Observability.MetricsEnabled {
		app.metrics = obsmetrics.New(clusterCfg.NodeID, fmt.Sprintf("node-%d", clusterCfg.NodeID))
		clusterCfg.Channel.Observer = combineChannelV2Observers(clusterCfg.Channel.Observer, channelV2MetricsObserver{metrics: app.metrics})
		clusterCfg.Storage.CommitObserver = combineCommitCoordinatorObservers(clusterCfg.Storage.CommitObserver, storageCommitMetricsObserver{metrics: app.metrics})
	}
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
	if app.api == nil && strings.TrimSpace(cfg.API.ListenAddr) != "" {
		app.api = accessapi.New(accessapi.Options{
			ListenAddr:           cfg.API.ListenAddr,
			Readyz:               app.readyzReport,
			BenchEnabled:         cfg.Bench.APIEnabled,
			BenchMaxBatchSize:    cfg.Bench.APIMaxBatchSize,
			BenchMaxPayloadBytes: cfg.Bench.APIMaxPayloadBytes,
			Gateway:              apiGatewayAddresses(cfg.API, cfg.Gateway.Listeners),
			BenchRuntime:         app.benchRuntimeController(),
			MetricsHandler:       app.metricsHandler(),
			PProfEnabled:         cfg.Observability.PProfEnabled,
		})
	}
	if app.gateway == nil && len(cfg.Gateway.Listeners) > 0 {
		gw, err := gateway.New(gateway.Options{
			Handler:        app.handler,
			Authenticator:  gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{NodeID: clusterCfg.NodeID}),
			Listeners:      cfg.Gateway.Listeners,
			DefaultSession: cfg.Gateway.Session,
			Transport:      cfg.Gateway.Transport,
			Observer:       app.gatewayObserver(),
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

// WithAPI overrides the HTTP API runtime.
func WithAPI(api APIRuntime) Option {
	return func(a *App) { a.api = api }
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

func (a *App) metricsHandler() http.Handler {
	if a == nil || a.metrics == nil {
		return nil
	}
	return a.metrics.Handler()
}

func (a *App) gatewayObserver() gateway.Observer {
	if a == nil || a.metrics == nil {
		return nil
	}
	return gatewayMetricsObserver{metrics: a.metrics}
}

func (a *App) benchRuntimeController() accessapi.ChannelRuntimeBenchController {
	if a == nil {
		return nil
	}
	node, ok := a.cluster.(clusterinfra.ChannelRuntimeBenchNode)
	if !ok {
		return nil
	}
	return clusterinfra.NewChannelRuntimeBenchController(node)
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

func apiGatewayAddresses(cfg APIConfig, listeners []gateway.ListenerOptions) accessapi.GatewayAddresses {
	addrs := gatewayAddressesFromListeners(listeners)
	if trimmed := strings.TrimSpace(cfg.ExternalTCPAddr); trimmed != "" {
		addrs.TCPAddr = trimmed
	}
	if trimmed := strings.TrimSpace(cfg.ExternalWSAddr); trimmed != "" {
		addrs.WSAddr = trimmed
	}
	if trimmed := strings.TrimSpace(cfg.ExternalWSSAddr); trimmed != "" {
		addrs.WSSAddr = trimmed
	}
	return addrs
}

func gatewayAddressesFromListeners(listeners []gateway.ListenerOptions) accessapi.GatewayAddresses {
	var out accessapi.GatewayAddresses
	for _, listener := range listeners {
		network := strings.ToLower(strings.TrimSpace(listener.Network))
		switch network {
		case "websocket":
			addr := normalizeWebsocketAddress(listener.Address)
			if strings.HasPrefix(strings.ToLower(addr), "wss://") {
				if out.WSSAddr == "" {
					out.WSSAddr = addr
				}
			} else if out.WSAddr == "" {
				out.WSAddr = addr
			}
		default:
			if out.TCPAddr == "" {
				out.TCPAddr = normalizeTCPAddress(listener.Address)
			}
		}
	}
	return out
}

func normalizeTCPAddress(addr string) string {
	trimmed := strings.TrimSpace(addr)
	return strings.TrimPrefix(trimmed, "tcp://")
}

func normalizeWebsocketAddress(addr string) string {
	trimmed := strings.TrimSpace(addr)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://") || trimmed == "" {
		return trimmed
	}
	return "ws://" + trimmed
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
