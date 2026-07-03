package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/benchdata"
	"github.com/gin-gonic/gin"
)

func (s *Server) registerBenchRoutes() {
	s.engine.GET("/bench/v1/capabilities", s.handleBenchCapabilities)
	s.engine.GET("/bench/v1/capacity-target", s.handleBenchCapacityTarget)
	s.engine.POST("/bench/v1/users/tokens", s.handleBenchTokens)
	s.engine.POST("/bench/v1/channels", s.handleBenchChannels)
	s.engine.POST("/bench/v1/channels/subscribers", s.handleBenchSubscribers)
	s.engine.GET("/bench/v1/snapshot", s.handleBenchSnapshot)
}

type benchCapacityTargetResponse struct {
	// Version is the benchmark API version that produced this target document.
	Version string `json:"version"`
	// Gateway contains gateway addresses published by this target node.
	Gateway benchCapacityGatewayResponse `json:"gateway"`
}

type benchCapacityGatewayResponse struct {
	// TCPAddr is the externally reachable WKProto TCP gateway address.
	TCPAddr string `json:"tcp_addr"`
	// WSAddr is the externally reachable WebSocket gateway address.
	WSAddr string `json:"ws_addr"`
	// WSSAddr is the externally reachable secure WebSocket gateway address.
	WSSAddr string `json:"wss_addr"`
}

func (s *Server) handleBenchCapabilities(c *gin.Context) {
	if s.benchData == nil {
		writeBenchError(c, http.StatusServiceUnavailable, "bench usecase not configured")
		return
	}
	c.JSON(http.StatusOK, s.benchData.Capabilities(c.Request.Context()))
}

func (s *Server) handleBenchCapacityTarget(c *gin.Context) {
	if c == nil {
		return
	}
	addr := s.legacyRouteExternal
	c.JSON(http.StatusOK, benchCapacityTargetResponse{
		Version: "bench/v1",
		Gateway: benchCapacityGatewayResponse{
			TCPAddr: addr.TCPAddr,
			WSAddr:  addr.WSAddr,
			WSSAddr: addr.WSSAddr,
		},
	})
}

func (s *Server) handleBenchTokens(c *gin.Context) {
	if s.benchData == nil {
		writeBenchError(c, http.StatusServiceUnavailable, "bench usecase not configured")
		return
	}
	var req benchdata.TokensRequest
	if !s.bindBenchJSON(c, &req) {
		return
	}
	resp, err := s.benchData.UpsertTokens(c.Request.Context(), req)
	if err != nil {
		writeBenchMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleBenchChannels(c *gin.Context) {
	if s.benchData == nil {
		writeBenchError(c, http.StatusServiceUnavailable, "bench usecase not configured")
		return
	}
	var req benchdata.ChannelsRequest
	if !s.bindBenchJSON(c, &req) {
		return
	}
	resp, err := s.benchData.UpsertChannels(c.Request.Context(), req)
	if err != nil {
		writeBenchMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleBenchSubscribers(c *gin.Context) {
	if s.benchData == nil {
		writeBenchError(c, http.StatusServiceUnavailable, "bench usecase not configured")
		return
	}
	var req benchdata.SubscribersRequest
	if !s.bindBenchJSON(c, &req) {
		return
	}
	resp, err := s.benchData.AddSubscribers(c.Request.Context(), req)
	if err != nil {
		writeBenchMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleBenchSnapshot(c *gin.Context) {
	if s.benchData == nil {
		writeBenchError(c, http.StatusServiceUnavailable, "bench usecase not configured")
		return
	}
	resp, err := s.benchData.Snapshot(c.Request.Context())
	if err != nil {
		writeBenchError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) bindBenchJSON(c *gin.Context, req any) bool {
	if s.benchMaxPayloadBytes > 0 {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, s.benchMaxPayloadBytes)
	}
	if err := c.ShouldBindJSON(req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeBenchError(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("payload too large: max %d bytes", maxBytesErr.Limit))
			return false
		}
		writeBenchError(c, http.StatusBadRequest, "invalid request")
		return false
	}
	return true
}

func writeBenchMutationError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, benchdata.ErrValidation) {
		status = http.StatusBadRequest
	} else if errors.Is(err, benchdata.ErrDependency) {
		status = http.StatusServiceUnavailable
	}
	writeBenchError(c, status, err.Error())
}

func writeBenchError(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"msg": msg, "status": status})
}
