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

	overview := s.engine.Group("/manager")
	if s.auth.enabled() {
		overview.Use(s.requirePermission("cluster.overview", "r"))
	}
	overview.GET("/overview", s.handleOverview)

	network := s.engine.Group("/manager")
	if s.auth.enabled() {
		network.Use(s.requirePermission("cluster.network", "r"))
	}
	network.GET("/network/summary", s.handleNetworkSummary)

	diagnostics := s.engine.Group("/manager")
	if s.auth.enabled() {
		diagnostics.Use(s.requirePermission("cluster.diagnostics", "r"))
	}
	diagnostics.GET("/diagnostics/trace/:trace_id", s.handleDiagnosticsTrace)
	diagnostics.GET("/diagnostics/message", s.handleDiagnosticsMessage)
	diagnostics.GET("/diagnostics/events", s.handleDiagnosticsEvents)

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

	channelRuntimeMeta := s.engine.Group("/manager")
	if s.auth.enabled() {
		channelRuntimeMeta.Use(s.requirePermission("cluster.channel", "r"))
	}
	channelRuntimeMeta.GET("/channel-runtime-meta", s.handleChannelRuntimeMeta)
	channelRuntimeMeta.GET("/channel-runtime-meta/:channel_type/:channel_id", s.handleChannelRuntimeMetaDetail)
	channelRuntimeMeta.GET("/messages", s.handleMessages)

	messageWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		messageWrites.Use(s.requirePermission("cluster.channel", "w"))
	}
	messageWrites.POST("/messages/retention", s.handleAdvanceMessageRetention)
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
