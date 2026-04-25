package api

import "github.com/gin-gonic/gin"

func (s *Server) handleHealthz(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
}

func (s *Server) handleHealthzDetails(c *gin.Context) {
	if s == nil || s.healthDetails == nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.JSON(200, s.healthDetails())
}

func (s *Server) handleReadyz(c *gin.Context) {
	if s == nil || s.readyz == nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	ready, body := s.readyz(c.Request.Context())
	if ready {
		c.JSON(200, body)
		return
	}
	c.JSON(503, body)
}
