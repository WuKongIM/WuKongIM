package server

import (
	"net/http"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// 这个主要为了模拟proxy模式。
type RouteAPI struct {
	s *Server
	wklog.Log
}

// NewRouteAPI NewRouteAPI
func NewRouteAPI(s *Server) *RouteAPI {
	return &RouteAPI{
		s:   s,
		Log: wklog.NewWKLog("RouteAPI"),
	}
}

// Route Route
func (a *RouteAPI) Route(r *wkhttp.WKHttp) {
	r.GET("/route", a.routeUserIMAddr)               // 获取用户所在节点的连接信息
	r.POST("/route/batch", a.routeUserIMAddrOfBatch) // 批量获取用户所在节点的连接信息

}

// 路由用户的IM连接地址
func (a *RouteAPI) routeUserIMAddr(c *wkhttp.Context) {

	intranet := wkutil.IntToBool(wkutil.ParseInt(c.Query("intranet"))) // 是否返回内网地址

	var (
		tcpAddr string
		wsAddr  string
		wssAddr string
	)

	if intranet {
		tcpAddr = a.s.opts.Intranet.TCPAddr
	} else {
		tcpAddr = a.s.opts.External.TCPAddr
		wsAddr = a.s.opts.External.WSAddr
		wssAddr = a.s.opts.External.WSSAddr
	}

	c.JSON(http.StatusOK, gin.H{
		"tcp_addr": tcpAddr,
		"ws_addr":  wsAddr,
		"wss_addr": wssAddr,
	})
}

// 批量获取用户所在节点地址
func (a *RouteAPI) routeUserIMAddrOfBatch(c *wkhttp.Context) {

	intranet := wkutil.IntToBool(wkutil.ParseInt(c.Query("intranet"))) // 是否返回内网地址

	var uids []string
	if err := c.BindJSON(&uids); err != nil {
		a.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(errors.New("数据格式有误！"))
		return
	}

	var (
		tcpAddr string
		wsAddr  string
		wssAddr string
	)

	if intranet {
		tcpAddr = a.s.opts.Intranet.TCPAddr
	} else {
		tcpAddr = a.s.opts.External.TCPAddr
		wsAddr = a.s.opts.External.WSAddr
		wssAddr = a.s.opts.External.WSSAddr
	}

	c.JSON(http.StatusOK, []userAddrResp{
		{
			UIDs:    uids,
			TCPAddr: tcpAddr,
			WSAddr:  wsAddr,
			WSSAddr: wssAddr,
		},
	})

}

type userAddrResp struct {
	TCPAddr string   `json:"tcp_addr"`
	WSAddr  string   `json:"ws_addr"`
	WSSAddr string   `json:"wss_addr"`
	UIDs    []string `json:"uids"`
}
