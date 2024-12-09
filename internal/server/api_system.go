package server

import (
	"net/http"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type SystemAPI struct {
	s *Server
	wklog.Log
}

func NewSystemAPI(s *Server) *SystemAPI {
	return &SystemAPI{
		s:   s,
		Log: wklog.NewWKLog("SystemAPI"),
	}
}

// Route route
func (s *SystemAPI) Route(r *wkhttp.WKHttp) {
	r.GET("/system/ping", s.ping)
}

func (s *SystemAPI) ping(c *wkhttp.Context) {
	pongs, err := s.s.cluster.TestPing()
	if err != nil {
		c.ResponseError(err)
		return
	}

	results := make([]pingResult, 0, len(pongs))
	for _, pong := range pongs {
		errStr := ""
		if pong.Err != nil {
			errStr = pong.Err.Error()
		}
		results = append(results, pingResult{
			NodeId:      pong.NodeId,
			Err:         errStr,
			Millisecond: pong.Millisecond,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].NodeId < results[j].NodeId
	})

	c.JSON(http.StatusOK, results)
}

type pingResult struct {
	NodeId      uint64 `json:"node_id"`
	Err         string `json:"err"`
	Millisecond int64  `json:"millisecond"`
}
