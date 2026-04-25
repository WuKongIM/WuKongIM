package api

import (
	"net/http/pprof"
	goruntimepprof "runtime/pprof"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleDebugConfig(c *gin.Context) {
	if s == nil || s.debugConfig == nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.JSON(200, s.debugConfig())
}

func (s *Server) handleDebugCluster(c *gin.Context) {
	if s == nil || s.debugCluster == nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.JSON(200, s.debugCluster())
}

func (s *Server) registerDebugRoutes() {
	if s == nil || s.engine == nil {
		return
	}
	s.engine.GET("/debug/goroutines", s.handleDebugGoroutines)
	s.engine.GET("/debug/pprof/", gin.WrapF(pprof.Index))
	s.engine.GET("/debug/pprof/cmdline", gin.WrapF(pprof.Cmdline))
	s.engine.GET("/debug/pprof/profile", gin.WrapF(pprof.Profile))
	s.engine.POST("/debug/pprof/symbol", gin.WrapF(pprof.Symbol))
	s.engine.GET("/debug/pprof/symbol", gin.WrapF(pprof.Symbol))
	s.engine.GET("/debug/pprof/trace", gin.WrapF(pprof.Trace))
	s.engine.GET("/debug/pprof/allocs", gin.WrapF(pprof.Index))
	s.engine.GET("/debug/pprof/block", gin.WrapF(pprof.Index))
	s.engine.GET("/debug/pprof/goroutine", gin.WrapF(pprof.Index))
	s.engine.GET("/debug/pprof/heap", gin.WrapF(pprof.Index))
	s.engine.GET("/debug/pprof/mutex", gin.WrapF(pprof.Index))
	s.engine.GET("/debug/pprof/threadcreate", gin.WrapF(pprof.Index))
}

func (s *Server) handleDebugGoroutines(c *gin.Context) {
	if profile := goruntimepprof.Lookup("goroutine"); profile != nil {
		c.Status(200)
		_ = profile.WriteTo(c.Writer, 1)
		return
	}
	c.JSON(500, gin.H{"error": "goroutine profile unavailable"})
}
