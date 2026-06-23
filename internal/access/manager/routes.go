package manager

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) registerRoutes() {
	if s == nil || s.engine == nil {
		return
	}

	s.engine.OPTIONS("/*path", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	if s.auth.enabled() {
		s.engine.POST("/manager/login", s.handleLogin)
	}
	nodes := s.engine.Group("/manager")
	if s.auth.enabled() {
		nodes.Use(s.requirePermission("cluster.node", "r"))
	}
	nodes.GET("/nodes", s.handleNodes)
	nodes.GET("/nodes/:node_id", s.handleNode)

	pluginReads := s.engine.Group("/manager")
	if s.auth.enabled() {
		pluginReads.Use(s.requirePermission("cluster.plugin", "r"))
	}
	pluginReads.GET("/nodes/:node_id/plugins", s.handleNodePlugins)
	pluginReads.GET("/nodes/:node_id/plugins/:plugin_no", s.handleNodePlugin)
	pluginReads.GET("/plugin-bindings", s.handlePluginBindings)

	pluginWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		pluginWrites.Use(s.requirePermission("cluster.plugin", "w"))
	}
	pluginWrites.PUT("/nodes/:node_id/plugins/:plugin_no/config", s.handleNodePluginConfigUpdate)
	pluginWrites.POST("/nodes/:node_id/plugins/:plugin_no/restart", s.handleNodePluginRestart)
	pluginWrites.DELETE("/nodes/:node_id/plugins/:plugin_no", s.handleNodePluginUninstall)
	pluginWrites.POST("/plugin-bindings", s.handlePluginBindingCreate)
	pluginWrites.DELETE("/plugin-bindings", s.handlePluginBindingDelete)

	nodeControllerRaft := s.engine.Group("/manager")
	if s.auth.enabled() {
		nodeControllerRaft.Use(s.requirePermission("cluster.node", "r"))
		nodeControllerRaft.Use(s.requirePermission("cluster.controller", "r"))
	}
	nodeControllerRaft.GET("/nodes/:node_id/controller-raft", s.handleNodeControllerRaft)

	nodeWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		nodeWrites.Use(s.requirePermission("cluster.node", "w"))
	}
	nodeWrites.POST("/nodes/:node_id/draining", s.handleNodeDraining)
	nodeWrites.POST("/nodes/:node_id/resume", s.handleNodeResume)

	scaleInReads := s.engine.Group("/manager")
	if s.auth.enabled() {
		scaleInReads.Use(s.requirePermission("cluster.node", "r"))
		scaleInReads.Use(s.requirePermission("cluster.slot", "r"))
	}
	scaleInReads.POST("/nodes/:node_id/scale-in/plan", s.handleNodeScaleInPlan)
	scaleInReads.GET("/nodes/:node_id/scale-in/status", s.handleNodeScaleInStatus)

	scaleInWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		scaleInWrites.Use(s.requirePermission("cluster.node", "w"))
		scaleInWrites.Use(s.requirePermission("cluster.slot", "w"))
	}
	scaleInWrites.POST("/nodes/:node_id/scale-in/start", s.handleNodeScaleInStart)
	scaleInWrites.POST("/nodes/:node_id/scale-in/advance", s.handleNodeScaleInAdvance)
	scaleInWrites.POST("/nodes/:node_id/scale-in/cancel", s.handleNodeScaleInCancel)

	slots := s.engine.Group("/manager")
	if s.auth.enabled() {
		slots.Use(s.requirePermission("cluster.slot", "r"))
	}
	slots.GET("/slots", s.handleSlots)
	slots.GET("/slots/:slot_id/logs", s.handleSlotLogs)
	slots.GET("/slots/:slot_id", s.handleSlot)

	slotWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		slotWrites.Use(s.requirePermission("cluster.slot", "w"))
	}
	slotWrites.POST("/slots", s.handleSlotAdd)
	slotWrites.DELETE("/slots/:slot_id", s.handleSlotDelete)
	slotWrites.POST("/slots/:slot_id/leader/transfer", s.handleSlotLeaderTransfer)
	slotWrites.POST("/slots/:slot_id/recover", s.handleSlotRecover)
	slotWrites.POST("/slots/rebalance", s.handleSlotRebalance)

	nodeSlotWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		nodeSlotWrites.Use(s.requirePermission("cluster.node", "w"))
		nodeSlotWrites.Use(s.requirePermission("cluster.slot", "w"))
	}
	nodeSlotWrites.POST("/nodes/:node_id/slots/:slot_id/compact", s.handleNodeSlotRaftCompact)

	onboardingCandidates := s.engine.Group("/manager")
	if s.auth.enabled() {
		onboardingCandidates.Use(s.requirePermission("cluster.node", "r"))
		onboardingCandidates.Use(s.requirePermission("cluster.slot", "r"))
	}
	onboardingCandidates.GET("/node-onboarding/candidates", s.handleNodeOnboardingCandidates)

	onboardingReads := s.engine.Group("/manager")
	if s.auth.enabled() {
		onboardingReads.Use(s.requirePermission("cluster.slot", "r"))
	}
	onboardingReads.GET("/node-onboarding/jobs", s.handleNodeOnboardingJobs)
	onboardingReads.GET("/node-onboarding/jobs/:job_id", s.handleNodeOnboardingJob)

	onboardingWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		onboardingWrites.Use(s.requirePermission("cluster.slot", "w"))
	}
	onboardingWrites.POST("/node-onboarding/plan", s.handleNodeOnboardingPlan)
	onboardingWrites.POST("/node-onboarding/jobs/:job_id/start", s.handleNodeOnboardingStart)
	onboardingWrites.POST("/node-onboarding/jobs/:job_id/retry", s.handleNodeOnboardingRetry)

	tasks := s.engine.Group("/manager")
	if s.auth.enabled() {
		tasks.Use(s.requirePermission("cluster.task", "r"))
	}
	tasks.GET("/tasks", s.handleTasks)
	tasks.GET("/tasks/:slot_id", s.handleTask)

	distributedTasks := s.engine.Group("/manager")
	if s.auth.enabled() {
		distributedTasks.Use(s.requirePermission("cluster.task", "r"))
	}
	distributedTasks.GET("/distributed-tasks/summary", s.handleDistributedTasksSummary)
	distributedTasks.GET("/distributed-tasks", s.handleDistributedTasks)
	distributedTasks.GET("/distributed-tasks/:domain/:id", s.handleDistributedTask)

	overview := s.engine.Group("/manager")
	if s.auth.enabled() {
		overview.Use(s.requirePermission("cluster.overview", "r"))
	}
	overview.GET("/overview", s.handleOverview)
	overview.GET("/dashboard/metrics", s.handleDashboardMetrics)

	network := s.engine.Group("/manager")
	if s.auth.enabled() {
		network.Use(s.requirePermission("cluster.network", "r"))
	}
	network.GET("/network/summary", s.handleNetworkSummary)

	permissions := s.engine.Group("/manager")
	if s.auth.enabled() {
		permissions.Use(s.requirePermission("cluster.permission", "r"))
	}
	permissions.GET("/permissions", s.handlePermissions)

	diagnostics := s.engine.Group("/manager")
	if s.auth.enabled() {
		diagnostics.Use(s.requirePermission("cluster.diagnostics", "r"))
	}
	diagnostics.GET("/diagnostics/trace/:trace_id", s.handleDiagnosticsTrace)
	diagnostics.GET("/diagnostics/message", s.handleDiagnosticsMessage)
	diagnostics.GET("/diagnostics/events", s.handleDiagnosticsEvents)
	diagnostics.GET("/diagnostics/tracking-rules", s.handleDiagnosticsTrackingRules)

	diagnosticsWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		diagnosticsWrites.Use(s.requirePermission("cluster.diagnostics", "w"))
	}
	diagnosticsWrites.POST("/diagnostics/tracking-rules", s.handleCreateDiagnosticsTrackingRule)
	diagnosticsWrites.DELETE("/diagnostics/tracking-rules/:rule_id", s.handleDeleteDiagnosticsTrackingRule)

	controllerLogs := s.engine.Group("/manager")
	if s.auth.enabled() {
		controllerLogs.Use(s.requirePermission("cluster.controller", "r"))
	}
	controllerLogs.GET("/controller/logs", s.handleControllerLogs)

	controllerRaftWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		controllerRaftWrites.Use(s.requirePermission("cluster.controller", "w"))
	}
	controllerRaftWrites.POST("/controller-raft/compact", s.handleControllerRaftCompact)
	controllerRaftWrites.POST("/nodes/:node_id/controller-raft/compact", s.handleNodeControllerRaftCompact)

	connections := s.engine.Group("/manager")
	if s.auth.enabled() {
		connections.Use(s.requirePermission("cluster.connection", "r"))
	}
	connections.GET("/connections", s.handleConnections)
	connections.GET("/connections/:session_id", s.handleConnection)

	userReads := s.engine.Group("/manager")
	if s.auth.enabled() {
		userReads.Use(s.requirePermission("cluster.user", "r"))
	}
	userReads.GET("/users", s.handleUsers)
	userReads.GET("/users/:uid", s.handleUser)

	userWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		userWrites.Use(s.requirePermission("cluster.user", "w"))
	}
	userWrites.POST("/users/:uid/kick", s.handleUserKick)
	userWrites.POST("/users/:uid/token/reset", s.handleUserTokenReset)

	systemUserReads := s.engine.Group("/manager")
	if s.auth.enabled() {
		systemUserReads.Use(s.requirePermission("cluster.user", "r"))
	}
	systemUserReads.GET("/system-users", s.handleSystemUsers)

	systemUserWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		systemUserWrites.Use(s.requirePermission("cluster.user", "w"))
	}
	systemUserWrites.POST("/system-users/add", s.handleSystemUsersAdd)
	systemUserWrites.POST("/system-users/remove", s.handleSystemUsersRemove)

	businessChannelReads := s.engine.Group("/manager")
	if s.auth.enabled() {
		businessChannelReads.Use(s.requirePermission("cluster.channel", "r"))
	}
	businessChannelReads.GET("/channels", s.handleBusinessChannels)
	businessChannelReads.GET("/channels/:channel_type/:channel_id", s.handleBusinessChannel)
	businessChannelReads.GET("/channels/:channel_type/:channel_id/subscribers", s.handleBusinessChannelSubscribers)
	businessChannelReads.GET("/channels/:channel_type/:channel_id/allowlist", s.handleBusinessChannelAllowlist)
	businessChannelReads.GET("/channels/:channel_type/:channel_id/denylist", s.handleBusinessChannelDenylist)

	businessChannelWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		businessChannelWrites.Use(s.requirePermission("cluster.channel", "w"))
	}
	businessChannelWrites.POST("/channels", s.handleBusinessChannelUpsert)
	businessChannelWrites.POST("/channels/:channel_type/:channel_id/subscribers/add", s.handleBusinessChannelSubscribersAdd)
	businessChannelWrites.POST("/channels/:channel_type/:channel_id/subscribers/remove", s.handleBusinessChannelSubscribersRemove)
	businessChannelWrites.POST("/channels/:channel_type/:channel_id/allowlist/add", s.handleBusinessChannelAllowlistAdd)
	businessChannelWrites.POST("/channels/:channel_type/:channel_id/allowlist/remove", s.handleBusinessChannelAllowlistRemove)
	businessChannelWrites.POST("/channels/:channel_type/:channel_id/denylist/add", s.handleBusinessChannelDenylistAdd)
	businessChannelWrites.POST("/channels/:channel_type/:channel_id/denylist/remove", s.handleBusinessChannelDenylistRemove)

	channelRuntimeMeta := s.engine.Group("/manager")
	if s.auth.enabled() {
		channelRuntimeMeta.Use(s.requirePermission("cluster.channel", "r"))
	}
	channelRuntimeMeta.GET("/channel-runtime-meta", s.handleChannelRuntimeMeta)
	channelRuntimeMeta.GET("/channel-runtime-meta/:channel_type/:channel_id", s.handleChannelRuntimeMetaDetail)
	channelRuntimeMeta.GET("/conversations", s.handleConversations)
	channelRuntimeMeta.GET("/messages", s.handleMessages)
	channelRuntimeMeta.GET("/channels/:channel_type/:channel_id/migration", s.handleChannelMigration)

	channelCluster := s.engine.Group("/manager")
	if s.auth.enabled() {
		channelCluster.Use(s.requirePermission("cluster.channel", "r"))
	}
	channelCluster.GET("/channel-cluster/summary", s.handleChannelClusterSummary)
	channelCluster.GET("/channel-cluster/unhealthy", s.handleChannelClusterUnhealthy)
	channelCluster.GET("/channel-cluster/:channel_type/:channel_id/replicas", s.handleChannelClusterReplicas)

	channelClusterWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		channelClusterWrites.Use(s.requirePermission("cluster.channel", "w"))
	}
	channelClusterWrites.POST("/channel-cluster/:channel_type/:channel_id/repair", s.handleChannelClusterRepair)
	channelClusterWrites.POST("/channel-cluster/:channel_type/:channel_id/leader/transfer", s.handleChannelClusterLeaderTransfer)

	messageWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		messageWrites.Use(s.requirePermission("cluster.channel", "w"))
	}
	messageWrites.POST("/messages/retention", s.handleAdvanceMessageRetention)
	messageWrites.POST("/channels/:channel_type/:channel_id/leader/transfer", s.handleChannelLeaderTransfer)
	messageWrites.POST("/channels/:channel_type/:channel_id/replicas/migrate", s.handleChannelReplicaMigrate)
	messageWrites.POST("/channels/:channel_type/:channel_id/migration/:task_id/abort", s.handleChannelMigrationAbort)
}

func openCORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		allowOrigin := "*"
		if c.Request != nil {
			if origin := c.Request.Header.Get("Origin"); origin != "" {
				allowOrigin = origin
				c.Writer.Header().Add("Vary", "Origin")
			}
		}
		c.Header("Access-Control-Allow-Origin", allowOrigin)
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Accept, Authorization, Token, X-Requested-With")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Expose-Headers", "Content-Length, Content-Type")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request != nil && c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func jsonError(c *gin.Context, status int, code, message string) {
	if message == "" {
		message = code
	}
	c.AbortWithStatusJSON(status, errorResponse{
		Error:   code,
		Message: message,
	})
}
