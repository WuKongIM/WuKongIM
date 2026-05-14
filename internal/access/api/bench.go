package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) registerBenchRoutes() {
	s.engine.GET("/bench/v1/capabilities", s.handleBenchCapabilities)
}

func (s *Server) handleBenchCapabilities(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"msg":    "bench usecase not configured",
		"status": http.StatusServiceUnavailable,
	})
}
