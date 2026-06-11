package api

import "github.com/gin-gonic/gin"

func (s *Server) registerRoutes() {
	if s == nil || s.engine == nil {
		return
	}

	s.engine.GET("/healthz", s.handleHealthz)
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
	if s.pluginRoutes != nil {
		s.engine.Any("/plugins/:plugin/*path", s.handlePluginRoute)
	}
	s.engine.GET("/route", s.handleRoute)
	s.engine.POST("/route/batch", s.handleRouteBatch)
	if s.benchEnabled {
		s.registerBenchRoutes()
	}
	if s.debugAPIEnabled {
		if s.debugConfig != nil {
			s.engine.GET("/debug/config", s.handleDebugConfig)
		}
		if s.debugCluster != nil {
			s.engine.GET("/debug/cluster", s.handleDebugCluster)
		}
		s.registerDebugRoutes()
		if s.diagnostics != nil {
			s.registerDiagnosticsRoutes()
		}
	}
	if s.testMode && s.testData != nil {
		s.registerTestDataRoutes()
	}
	s.engine.POST("/user/token", s.handleUpdateToken)
	s.engine.POST("/user/device_quit", s.handleDeviceQuit)
	s.engine.POST("/user/onlinestatus", s.handleOnlineStatus)
	s.engine.POST("/user/systemuids_add", s.handleSystemUIDsAdd)
	s.engine.POST("/user/systemuids_remove", s.handleSystemUIDsRemove)
	s.engine.GET("/user/systemuids", s.handleSystemUIDsGet)
	s.engine.POST("/user/systemuids_add_to_cache", s.handleSystemUIDsAddToCache)
	s.engine.POST("/user/systemuids_remove_from_cache", s.handleSystemUIDsRemoveFromCache)
	s.engine.POST("/channel", s.handleChannelUpsert)
	s.engine.POST("/channel/info", s.handleChannelInfo)
	s.engine.POST("/channel/delete", s.handleChannelDelete)
	s.engine.POST("/channel/subscriber_add", s.handleChannelSubscriberAdd)
	s.engine.POST("/channel/subscriber_remove", s.handleChannelSubscriberRemove)
	s.engine.POST("/channel/subscriber_remove_all", s.handleChannelSubscriberRemoveAll)
	s.engine.POST("/tmpchannel/subscriber_set", s.handleTmpChannelSubscriberSet)
	s.engine.POST("/channel/blacklist_add", s.handleChannelDenylistAdd)
	s.engine.POST("/channel/blacklist_set", s.handleChannelDenylistSet)
	s.engine.POST("/channel/blacklist_remove", s.handleChannelDenylistRemove)
	s.engine.POST("/channel/blacklist_remove_all", s.handleChannelDenylistRemoveAll)
	s.engine.POST("/channel/whitelist_add", s.handleChannelAllowlistAdd)
	s.engine.POST("/channel/whitelist_set", s.handleChannelAllowlistSet)
	s.engine.POST("/channel/whitelist_remove", s.handleChannelAllowlistRemove)
	s.engine.POST("/channel/whitelist_remove_all", s.handleChannelAllowlistRemoveAll)
	s.engine.GET("/channel/whitelist", s.handleChannelAllowlistGet)
	s.engine.POST("/message/send", s.handleSendMessage)
	s.engine.POST("/message/sync", s.handleMessageSync)
	s.engine.POST("/message/syncack", s.handleMessageSyncAck)
	s.engine.POST("/channel/messagesync", s.handleChannelMessageSync)
	s.engine.POST("/conversations/clearUnread", s.handleConversationClearUnread)
	s.engine.POST("/conversations/setUnread", s.handleConversationSetUnread)
	s.engine.POST("/conversations/delete", s.handleConversationDelete)
	s.engine.POST("/conversation/sync", s.handleConversationSync)
}
