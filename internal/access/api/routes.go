package api

import "github.com/gin-gonic/gin"

func (s *Server) registerRoutes() {
	if s == nil || s.engine == nil {
		return
	}

	s.engine.GET("/healthz", s.handleHealthz)
	s.engine.OPTIONS("/*path", func(c *gin.Context) {
		c.Status(204)
	})
	if s.healthDetailEnabled && s.healthDetails != nil {
		s.engine.GET("/healthz/details", s.handleHealthzDetails)
	}
	if s.readyz != nil {
		s.engine.GET("/readyz", s.handleReadyz)
	}
	if s.metricsHandler != nil {
		s.engine.GET("/metrics", func(c *gin.Context) {
			s.metricsHandler.ServeHTTP(c.Writer, c.Request)
		})
	}
	s.engine.GET("/route", s.handleRoute)
	s.engine.POST("/route/batch", s.handleRouteBatch)
	if s.debugEnabled {
		if s.debugConfig != nil {
			s.engine.GET("/debug/config", s.handleDebugConfig)
		}
		if s.debugCluster != nil {
			s.engine.GET("/debug/cluster", s.handleDebugCluster)
		}
		s.registerDebugRoutes()
	}
	s.engine.POST("/user/token", s.handleUpdateToken)
	s.engine.POST("/message/send", s.handleSendMessage)
	s.engine.POST("/conversation/sync", s.handleConversationSync)
}
