package app

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	obsdiagnostics "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	accessgateway "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	authoritypresence "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
	channelusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/channel"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	userusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/bwmarrin/snowflake"
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

// WorkerRuntime is a background app worker managed inside the lifecycle.
type WorkerRuntime interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// Option customizes App construction.
type Option func(*App)

// App is the internalv2 composition root for cluster, message, and gateway runtimes.
type App struct {
	cfg                         Config
	cluster                     ClusterRuntime
	api                         APIRuntime
	gateway                     GatewayRuntime
	handler                     *accessgateway.Handler
	messages                    *message.App
	apiMessages                 accessapi.MessageUsecase
	channelAppends              *channelappend.Group
	channelAppendRouter         *channelappend.Router
	channelAppendDeliveryWorker *channelappend.RecipientDeliveryWorker
	channelAppendMetadata       *clusterinfra.ChannelAppendMetadataCache
	channels                    *channelusecase.App
	conversations               *conversationusecase.App
	users                       *userusecase.App
	delivery                    *deliveryusecase.App
	deliveryManager             *runtimedelivery.Manager
	deliveryRetry               *runtimedelivery.RetryScheduler
	deliveryWorker              WorkerRuntime
	localOwnerPusher            *localOwnerPusher
	conversationRouteLifecycle  WorkerRuntime
	conversationActiveWorker    WorkerRuntime
	conversationAuthority       *conversationAuthority
	conversationAuthorityClient *clusterinfra.ConversationAuthorityClient
	// deliverySubscribers scans durable non-person channel subscribers when provided.
	deliverySubscribers runtimedelivery.ChannelSubscriberSource
	deliveryMeta        *deliveryMetaStore
	presence            *presence.App
	online              *online.Registry
	presenceDirectory   *authoritypresence.Directory
	presenceWorker      WorkerRuntime
	metrics             *obsmetrics.Registry
	// diagnostics stores sampled send-path trace events for app-local queries.
	diagnostics *obsdiagnostics.Store
	// diagnosticsTracking stores dynamic diagnostics sampling rules.
	diagnosticsTracking *obsdiagnostics.TrackingRules
	// diagnosticsRestore restores the process-wide sendtrace sink installed by this app.
	diagnosticsRestore func()
	logger             wklog.Logger

	lifecycleMu               sync.Mutex
	started                   bool
	stopped                   bool
	clusterStarted            bool
	presenceStarted           bool
	conversationRouteStarted  bool
	conversationActiveStarted bool
	channelAppendStarted      bool
	deliveryStarted           bool
	apiStarted                bool
	gatewayStarted            bool
	deliveryErrors            atomic.Uint64
}

// New creates an internalv2 App.
func New(cfg Config, opts ...Option) (*App, error) {
	app := &App{cfg: cfg}
	constructionOK := false
	defer func() {
		if !constructionOK {
			app.restoreDiagnosticsSink()
		}
	}()

	if err := app.applyConfigDefaults(); err != nil {
		return nil, err
	}
	app.applyOptions(opts)
	if err := app.ensureLogger(); err != nil {
		return nil, err
	}

	clusterCfg := defaultClusterConfig(app.cfg)
	app.configureObservability(&clusterCfg)
	if err := app.ensureCluster(clusterCfg); err != nil {
		return nil, err
	}

	app.ensureOnlineRegistry()
	app.wireDeliveryMetadata()
	app.wireChannels()
	conversationReadStore := app.newConversationReadStore()
	app.wireConversationAuthority()
	app.wireConversations(conversationReadStore)
	app.wirePresence()
	app.wireUsers()
	app.wireDelivery()
	if err := app.wireChannelAppend(clusterCfg.NodeID); err != nil {
		return nil, err
	}
	app.wireMessages()
	app.wireAPIMessageFacade()
	app.wireGatewayHandler(clusterCfg.NodeID)
	app.wireAPI()
	if err := app.wireGateway(clusterCfg.NodeID); err != nil {
		return nil, err
	}

	constructionOK = true
	return app, nil
}

func commitCoordinatorWorkerCount(shards int) int {
	if shards <= 1 {
		return dbMessageCommitWorkerCap
	}
	return shards
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

// WithChannels overrides the channel usecase app.
func WithChannels(channels *channelusecase.App) Option {
	return func(a *App) { a.channels = channels }
}

// WithConversations overrides the conversation usecase app.
func WithConversations(conversations *conversationusecase.App) Option {
	return func(a *App) { a.conversations = conversations }
}

// WithPresence overrides the presence usecase app.
func WithPresence(presence *presence.App) Option {
	return func(a *App) { a.presence = presence }
}

// WithOnlineRegistry overrides the owner-local online registry.
func WithOnlineRegistry(reg *online.Registry) Option {
	return func(a *App) { a.online = reg }
}

// WithDeliverySubscriberSource overrides the durable subscriber source used by delivery fanout.
func WithDeliverySubscriberSource(source runtimedelivery.ChannelSubscriberSource) Option {
	return func(a *App) { a.deliverySubscribers = source }
}

// WithLogger overrides the root logger used by the app.
func WithLogger(logger wklog.Logger) Option {
	return func(a *App) { a.logger = logger }
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

// Conversations returns the conversation list usecase app.
func (a *App) Conversations() *conversationusecase.App {
	if a == nil {
		return nil
	}
	return a.conversations
}

// Delivery returns the delivery usecase app.
func (a *App) Delivery() *deliveryusecase.App {
	if a == nil {
		return nil
	}
	return a.delivery
}

func (a *App) metricsHandler() http.Handler {
	if a == nil || a.metrics == nil {
		return nil
	}
	return a.metrics.Handler()
}

func (a *App) conversationListObserver() accessapi.ConversationListObserver {
	if a == nil || a.metrics == nil {
		return nil
	}
	return conversationListMetricsObserver{metrics: a.metrics}
}

func (a *App) conversationAuthorityObserver() conversationAuthorityObserver {
	if a == nil || a.metrics == nil {
		return nil
	}
	return conversationAuthorityMetricsObserver{metrics: a.metrics}
}

func (a *App) gatewayObserver() gateway.Observer {
	if a == nil || a.metrics == nil {
		return nil
	}
	return gatewayMetricsObserver{metrics: a.metrics}
}

func (a *App) sendackObserver() accessgateway.SendackObserver {
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

func (a *App) gatewayPresenceUsecase() accessgateway.PresenceUsecase {
	if a == nil || a.presence == nil {
		return nil
	}
	if a.cfg.Presence.ActivationTimeout <= 0 {
		return a.presence
	}
	return activationTimeoutPresence{
		next:    a.presence,
		timeout: a.cfg.Presence.ActivationTimeout,
	}
}

func (a *App) currentPresenceAuthorities() []clusterv2.RouteAuthority {
	routes, ok := a.cluster.(clusterWriteReadyRuntime)
	if !ok {
		return nil
	}
	snapshot := routes.Snapshot()
	if snapshot.HashSlotCount == 0 {
		return nil
	}
	authorities := make([]clusterv2.RouteAuthority, 0, snapshot.HashSlotCount)
	for hashSlot := uint16(0); hashSlot < snapshot.HashSlotCount; hashSlot++ {
		route, err := routes.RouteHashSlot(hashSlot)
		if err != nil {
			continue
		}
		authorities = append(authorities, clusterv2.RouteAuthority{
			HashSlot:       route.HashSlot,
			SlotID:         route.SlotID,
			LeaderNodeID:   route.Leader,
			RouteRevision:  route.Revision,
			AuthorityEpoch: route.AuthorityEpoch,
		})
	}
	return authorities
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

func legacyRouteAddresses(apiCfg APIConfig, listeners []gateway.ListenerOptions) (accessapi.LegacyRouteAddresses, accessapi.LegacyRouteAddresses) {
	external, intranet := legacyRouteAddressesFromListeners(listeners)
	if trimmed := strings.TrimSpace(apiCfg.ExternalTCPAddr); trimmed != "" {
		external.TCPAddr = trimmed
	}
	if trimmed := strings.TrimSpace(apiCfg.ExternalWSAddr); trimmed != "" {
		external.WSAddr = trimmed
	}
	if trimmed := strings.TrimSpace(apiCfg.ExternalWSSAddr); trimmed != "" {
		external.WSSAddr = trimmed
	}
	return external, intranet
}

func legacyRouteAddressesFromListeners(listeners []gateway.ListenerOptions) (accessapi.LegacyRouteAddresses, accessapi.LegacyRouteAddresses) {
	var external accessapi.LegacyRouteAddresses
	var intranet accessapi.LegacyRouteAddresses
	for _, listener := range listeners {
		network := strings.ToLower(strings.TrimSpace(listener.Network))
		switch network {
		case "websocket":
			addr := normalizeWebsocketAddress(listener.Address)
			switch {
			case strings.HasPrefix(strings.ToLower(addr), "wss://"):
				if external.WSSAddr == "" {
					external.WSSAddr = addr
				}
			case external.WSAddr == "":
				external.WSAddr = addr
			}
		default:
			if external.TCPAddr == "" {
				external.TCPAddr = normalizeTCPAddress(listener.Address)
			}
			if intranet.TCPAddr == "" {
				intranet.TCPAddr = normalizeTCPAddress(listener.Address)
			}
		}
	}
	return external, intranet
}

func legacyRouteNodeAddresses(localNodeID uint64, voters []clusterv2.ControlVoter, external, intranet accessapi.LegacyRouteAddresses) map[uint64]accessapi.LegacyRouteNodeAddresses {
	out := make(map[uint64]accessapi.LegacyRouteNodeAddresses, len(voters)+1)
	if localNodeID != 0 {
		out[localNodeID] = accessapi.LegacyRouteNodeAddresses{External: external, Intranet: intranet}
	}
	for _, voter := range voters {
		if voter.NodeID == 0 || voter.NodeID == localNodeID {
			continue
		}
		host := legacyRouteNodeHost(voter.Addr)
		if host == "" {
			continue
		}
		out[voter.NodeID] = accessapi.LegacyRouteNodeAddresses{
			External: legacyRouteAddressesForHost(external, host),
			Intranet: legacyRouteAddressesForHost(intranet, host),
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func legacyRouteAddressesForHost(addrs accessapi.LegacyRouteAddresses, host string) accessapi.LegacyRouteAddresses {
	return accessapi.LegacyRouteAddresses{
		TCPAddr: legacyRouteHostPort(addrs.TCPAddr, host),
		WSAddr:  legacyRouteURLHost(addrs.WSAddr, host),
		WSSAddr: legacyRouteURLHost(addrs.WSSAddr, host),
	}
}

func legacyRouteNodeHost(addr string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return ""
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" {
		return legacyRouteHostOnly(parsed.Host)
	}
	trimmed = strings.TrimPrefix(trimmed, "tcp://")
	return legacyRouteHostOnly(trimmed)
}

func legacyRouteHostOnly(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	if strings.Contains(err.Error(), "missing port in address") {
		return strings.Trim(addr, "[]")
	}
	return ""
}

func legacyRouteHostPort(addr string, host string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" || strings.TrimSpace(host) == "" {
		return trimmed
	}
	_, port, err := net.SplitHostPort(trimmed)
	if err != nil {
		return trimmed
	}
	return net.JoinHostPort(host, port)
}

func legacyRouteURLHost(addr string, host string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" || strings.TrimSpace(host) == "" {
		return trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return trimmed
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err == nil {
		parsed.Host = net.JoinHostPort(host, port)
		return parsed.String()
	}
	if strings.Contains(err.Error(), "missing port in address") {
		parsed.Host = host
		return parsed.String()
	}
	return trimmed
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

// nodeMessageIDs allocates node-scoped Snowflake message ids.
type nodeMessageIDs struct {
	// node is the Snowflake generator bound to the effective cluster node id.
	node *snowflake.Node
}

func newNodeMessageIDs(nodeID uint64) (*nodeMessageIDs, error) {
	node, err := snowflake.NewNode(int64(nodeID))
	if err != nil {
		return nil, err
	}
	return &nodeMessageIDs{node: node}, nil
}

func (g *nodeMessageIDs) Next() uint64 {
	return uint64(g.node.Generate())
}

type nodeRPCHandlerFunc func(context.Context, []byte) ([]byte, error)

func (f nodeRPCHandlerFunc) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return f(ctx, payload)
}

type nodeRPCRegistrar interface {
	RegisterRPC(uint8, clusterv2.NodeRPCHandler)
}

type presenceDirectoryAuthority struct {
	// directory stores authoritative virtual routes for locally led hash slots.
	directory *authoritypresence.Directory
}

type presenceOwnerActions struct {
	// local stores real sessions owned by this node.
	local presenceOwnerLocalRegistry
}

type activationTimeoutPresence struct {
	// next is the underlying entry-agnostic presence usecase.
	next accessgateway.PresenceUsecase
	// timeout bounds authority registration during gateway activation.
	timeout time.Duration
}

func (p activationTimeoutPresence) Activate(ctx context.Context, cmd presence.ActivateCommand) error {
	if p.next == nil {
		return nil
	}
	if p.timeout <= 0 {
		return p.next.Activate(ctx, cmd)
	}
	activateCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return p.next.Activate(activateCtx, cmd)
}

func (p activationTimeoutPresence) Deactivate(ctx context.Context, cmd presence.DeactivateCommand) error {
	if p.next == nil {
		return nil
	}
	return p.next.Deactivate(ctx, cmd)
}

func (p activationTimeoutPresence) Touch(ctx context.Context, cmd presence.TouchCommand) error {
	if p.next == nil {
		return nil
	}
	return p.next.Touch(ctx, cmd)
}

func (a presenceDirectoryAuthority) RegisterRoute(ctx context.Context, target presence.RouteTarget, route presence.Route) (presence.RegisterResult, error) {
	if err := ctx.Err(); err != nil {
		return presence.RegisterResult{}, err
	}
	return a.directory.RegisterRoute(target, route)
}

func (a presenceDirectoryAuthority) CommitRoute(ctx context.Context, target presence.RouteTarget, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.directory.CommitRoute(target, presence.PendingRouteToken(token))
}

func (a presenceDirectoryAuthority) AbortRoute(ctx context.Context, target presence.RouteTarget, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.directory.AbortRoute(target, presence.PendingRouteToken(token))
}

func (a presenceDirectoryAuthority) UnregisterRoute(ctx context.Context, target presence.RouteTarget, identity presence.RouteIdentity, ownerSeq uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.directory.UnregisterRoute(target, identity, ownerSeq)
}

func (a presenceDirectoryAuthority) EndpointsByUID(ctx context.Context, target presence.RouteTarget, uid string) ([]presence.Route, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return a.directory.EndpointsByUID(target, uid)
}

func (a presenceDirectoryAuthority) TouchRoutes(ctx context.Context, target presence.RouteTarget, routes []presence.Route) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.directory.TouchRoutes(target, routes)
}

func (a presenceOwnerActions) ApplyRouteAction(ctx context.Context, action presence.RouteAction) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.local == nil {
		return authoritypresence.ErrRouteNotReady
	}
	session, ok := a.local.LocalSession(action.SessionID)
	route := session.Route
	if !ok || route.UID != action.UID || route.OwnerNodeID != action.OwnerNodeID || route.OwnerBootID != action.OwnerBootID {
		return nil
	}
	if session.Session != nil {
		if err := session.Session.CloseSession(action.Reason); err != nil {
			return err
		}
	}
	a.local.MarkClosingAndUnregister(action.SessionID)
	return nil
}

func newOwnerBootID() uint64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		if id := binary.LittleEndian.Uint64(buf[:]); id != 0 {
			return id
		}
	}
	return uint64(time.Now().UnixNano())
}
