package api

import (
	"net/http"

	testdatausecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/testdata"
	"github.com/gin-gonic/gin"
)

type generateSlotSnapshotUsersRequest struct {
	Prefix       string `json:"prefix"`
	Count        int    `json:"count"`
	PayloadBytes int    `json:"payload_bytes"`
	Seed         string `json:"seed"`
	DeviceFlag   uint8  `json:"device_flag"`
	DeviceLevel  uint8  `json:"device_level"`
}

type generateControllerSnapshotJobsRequest struct {
	Prefix       string `json:"prefix"`
	TargetNodeID uint64 `json:"target_node_id"`
	Count        int    `json:"count"`
	PayloadBytes int    `json:"payload_bytes"`
	Seed         string `json:"seed"`
}

func (s *Server) registerTestDataRoutes() {
	s.engine.POST("/testdata/e2e/cluster/slot-snapshot-users", s.handleGenerateSlotSnapshotUsers)
	s.engine.POST("/testdata/e2e/cluster/controller-snapshot-jobs", s.handleGenerateControllerSnapshotJobs)
}

func (s *Server) handleGenerateSlotSnapshotUsers(c *gin.Context) {
	var req generateSlotSnapshotUsersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "invalid request", "status": http.StatusBadRequest})
		return
	}
	if s == nil || s.testData == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"msg": "test data usecase not configured", "status": http.StatusServiceUnavailable})
		return
	}

	result, err := s.testData.GenerateSlotSnapshotUsers(c.Request.Context(), testdatausecase.GenerateSlotSnapshotUsersCommand{
		Prefix:       req.Prefix,
		Count:        req.Count,
		PayloadBytes: req.PayloadBytes,
		Seed:         req.Seed,
		DeviceFlag:   req.DeviceFlag,
		DeviceLevel:  req.DeviceLevel,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": err.Error(), "status": http.StatusBadRequest})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (s *Server) handleGenerateControllerSnapshotJobs(c *gin.Context) {
	var req generateControllerSnapshotJobsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "invalid request", "status": http.StatusBadRequest})
		return
	}
	if s == nil || s.testData == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"msg": "test data usecase not configured", "status": http.StatusServiceUnavailable})
		return
	}

	result, err := s.testData.GenerateControllerSnapshotJobs(c.Request.Context(), testdatausecase.GenerateControllerSnapshotJobsCommand{
		Prefix:       req.Prefix,
		TargetNodeID: req.TargetNodeID,
		Count:        req.Count,
		PayloadBytes: req.PayloadBytes,
		Seed:         req.Seed,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": err.Error(), "status": http.StatusBadRequest})
		return
	}
	c.JSON(http.StatusOK, result)
}
