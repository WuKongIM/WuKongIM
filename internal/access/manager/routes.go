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

	nodeWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		nodeWrites.Use(s.requirePermission("cluster.node", "w"))
	}
	nodeWrites.POST("/nodes/:node_id/draining", s.handleNodeDraining)
	nodeWrites.POST("/nodes/:node_id/resume", s.handleNodeResume)

	slots := s.engine.Group("/manager")
	if s.auth.enabled() {
		slots.Use(s.requirePermission("cluster.slot", "r"))
	}
	slots.GET("/slots", s.handleSlots)
	slots.GET("/slots/:slot_id", s.handleSlot)

	slotWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		slotWrites.Use(s.requirePermission("cluster.slot", "w"))
	}
	slotWrites.POST("/slots/:slot_id/leader/transfer", s.handleSlotLeaderTransfer)
	slotWrites.POST("/slots/:slot_id/recover", s.handleSlotRecover)
	slotWrites.POST("/slots/rebalance", s.handleSlotRebalance)

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
}

func openCORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
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
