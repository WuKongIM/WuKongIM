package server

import (
	"errors"
	"net/http"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type SystemAPI struct {
	wklog.Log
	s *Server
}

// NewSystemAPI 创建API
func NewSystemAPI(s *Server) *SystemAPI {
	return &SystemAPI{
		Log: wklog.NewWKLog("ChannelAPI"),
		s:   s,
	}
}

// Route Route
func (s *SystemAPI) Route(r *wkhttp.WKHttp) {
	r.POST("/system/ip/blacklist_add", s.ipBlacklistAdd)       // 添加ip黑名单
	r.POST("/system/ip/blacklist_remove", s.ipBlacklistRemove) // 移除ip白名单
	r.GET("/system/ip/blacklist", s.ipBlacklist)               // 获取ip黑名单列表
}

func (s *SystemAPI) ipBlacklistAdd(c *wkhttp.Context) {
	var req struct {
		IPs []string `json:"ips"`
	}
	if err := c.BindJSON(&req); err != nil {
		s.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if len(req.IPs) == 0 {
		s.Error("ip列表不能为空！")
		c.ResponseError(errors.New("ip列表不能为空！"))
		return
	}
	err := s.s.store.AddIPBlacklist(req.IPs)
	if err != nil {
		s.Error("添加ip黑名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	s.s.AddIPBlacklist(req.IPs)
	c.ResponseOK()
}

func (s *SystemAPI) ipBlacklistRemove(c *wkhttp.Context) {
	var req struct {
		IPs []string `json:"ips"`
	}
	if err := c.BindJSON(&req); err != nil {
		s.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if len(req.IPs) == 0 {
		s.Error("ip列表不能为空！")
		c.ResponseError(errors.New("ip列表不能为空！"))
		return
	}
	err := s.s.store.RemoveIPBlacklist(req.IPs)
	if err != nil {
		s.Error("移除ip黑名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	s.s.RemoveIPBlacklist(req.IPs)
	c.ResponseOK()
}

func (s *SystemAPI) ipBlacklist(c *wkhttp.Context) {
	ips, err := s.s.store.GetIPBlacklist()
	if err != nil {
		s.Error("获取ip黑名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	c.JSON(http.StatusOK, ips)
}
