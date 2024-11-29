package server

import (
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/pse"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/WuKongIM/WuKongIM/version"
	"go.uber.org/zap"
)

type VarzAPI struct {
	wklog.Log
	s *Server
}

func NewVarzAPI(s *Server) *VarzAPI {

	return &VarzAPI{
		s:   s,
		Log: wklog.NewWKLog("VarzAPI"),
	}
}

func (v *VarzAPI) Route(r *wkhttp.WKHttp) {
	r.GET("/varz", v.HandleVarz) // 获取系统变量

	r.GET("/varz/setting", v.Settings) // 获取系统设置
}

func (v *VarzAPI) HandleVarz(c *wkhttp.Context) {

	show := c.Query("show")
	connLimit, _ := strconv.Atoi(c.Query("conn_limit"))
	nodeId := wkutil.ParseUint64(c.Query("node_id"))

	if nodeId > 0 && nodeId != v.s.opts.Cluster.NodeId {
		node, err := v.s.clusterServer.NodeInfoById(nodeId)
		if err != nil {
			c.ResponseError(err)
			return
		}
		if node == nil {
			v.Error("node not found", zap.Uint64("nodeId", nodeId))
			c.ResponseError(fmt.Errorf("node not found"))
			return
		}
		c.ForwardWithBody(fmt.Sprintf("%s%s", node.ApiServerAddr, c.Request.URL.Path), nil)
		return
	}

	if connLimit == 0 {
		connLimit = 20
	}
	varz := CreateVarz(v.s)

	if show == "conn" {
		resultConns := v.s.GetConnInfos("", ByInMsgDesc, 0, connLimit)
		connInfos := make([]*ConnInfo, 0, len(resultConns))
		for _, resultConn := range resultConns {
			if resultConn == nil || !resultConn.isAuth.Load() {
				continue
			}
			connInfos = append(connInfos, newConnInfo(resultConn))
		}
		varz.Conns = connInfos
	}

	c.JSON(http.StatusOK, varz)
}

func (v *VarzAPI) Settings(c *wkhttp.Context) {

	setting := &SystemSetting{}
	setting.Logger.TraceOn = wkutil.BoolToInt(v.s.opts.Logger.TraceOn)
	setting.Logger.LokiOn = wkutil.BoolToInt(v.s.opts.LokiOn())
	setting.PrometheusOn = wkutil.BoolToInt(v.s.opts.PrometheusOn())
	setting.StressOn = wkutil.BoolToInt(v.s.opts.Stress)

	c.JSON(http.StatusOK, setting)
}

func CreateVarz(s *Server) *Varz {
	var rss, vss int64 // rss内存 vss虚拟内存
	var pcpu float64   // cpu
	err := pse.ProcUsage(&pcpu, &rss, &vss)
	if err != nil {
		s.Error("获取系统资源失败", zap.Error(err))
	}
	opts := s.opts
	connCount := s.engine.ConnCount()

	app := s.trace.Metrics.App()
	inMsgs := app.SendPacketCount() + app.SendackPacketCount() + app.RecvackPacketCount() + app.PingBytes() + app.ConnPacketCount()
	outMsgs := app.RecvPacketCount() + app.RecvackPacketCount() + app.PongBytes() + app.ConnackPacketCount()
	inBytes := app.SendPacketBytes() + app.SendackPacketBytes() + app.RecvackPacketBytes() + app.PingBytes() + app.ConnPacketBytes()
	outBytes := app.RecvPacketBytes() + app.RecvackPacketBytes() + app.PongBytes() + app.ConnackPacketBytes()

	return &Varz{
		ServerID:             fmt.Sprintf("%d", opts.Cluster.NodeId),
		ServerName:           "WuKongIM",
		Version:              version.Version,
		Connections:          connCount,
		UserHandlerCount:     s.userReactor.getHandlerCount(),
		UserHandlerConnCount: s.userReactor.getAllConnCount(),
		Uptime:               myUptime(time.Since(s.start)),
		CPU:                  pcpu,
		Goroutine:            runtime.NumGoroutine(),
		Mem:                  rss,
		InMsgs:               inMsgs,
		OutMsgs:              outMsgs,
		InBytes:              inBytes,
		OutBytes:             outBytes,
		RetryQueue:           int64(s.retryManager.retryMessageCount()),

		TCPAddr:     opts.External.TCPAddr,
		WSAddr:      opts.External.WSAddr,
		WSSAddr:     opts.External.WSSAddr,
		ManagerAddr: opts.External.ManagerAddr,
		ManagerOn:   wkutil.BoolToInt(opts.Manager.On),

		APIURL:         opts.External.APIUrl,
		Commit:         version.Commit,
		CommitDate:     version.CommitDate,
		TreeState:      version.TreeState,
		ManagerUID:     opts.ManagerUID,
		ManagerTokenOn: wkutil.BoolToInt(opts.ManagerTokenOn),

		ConversationCacheCount: s.conversationManager.ConversationCount(),
	}
}

type Varz struct {
	ServerID             string  `json:"server_id"`               // 服务端ID
	ServerName           string  `json:"server_name"`             // 服务端名称
	Version              string  `json:"version"`                 // 服务端版本
	Connections          int     `json:"connections"`             // 当前连接数量
	UserHandlerCount     int     `json:"user_handler_count"`      // 用户处理者数量
	UserHandlerConnCount int     `json:"user_handler_conn_count"` // 所有用户处理者连接数量
	Uptime               string  `json:"uptime"`                  // 上线时间
	Goroutine            int     `json:"goroutine"`               // goroutine数量
	Mem                  int64   `json:"mem"`                     // 内存
	CPU                  float64 `json:"cpu"`                     // cpu

	InMsgs      int64 `json:"in_msgs"`      // 流入消息数量
	OutMsgs     int64 `json:"out_msgs"`     // 流出消息数量
	InBytes     int64 `json:"in_bytes"`     // 流入字节数量
	OutBytes    int64 `json:"out_bytes"`    // 流出字节数量
	SlowClients int64 `json:"slow_clients"` // 慢客户端数量
	RetryQueue  int64 `json:"retry_queue"`  // 重试队列数量

	TCPAddr     string `json:"tcp_addr"`     // tcp地址
	WSAddr      string `json:"ws_addr"`      // ws地址
	WSSAddr     string `json:"wss_addr"`     // wss地址
	ManagerAddr string `json:"manager_addr"` // 管理地址
	ManagerOn   int    `json:"manager_on"`   // 管理是否开启
	Commit      string `json:"commit"`       // git commit id
	CommitDate  string `json:"commit_date"`  // git commit date
	TreeState   string `json:"tree_state"`   // git tree state
	APIURL      string `json:"api_url"`      // api地址

	ManagerUID             string      `json:"manager_uid"`              // 管理员uid
	ManagerTokenOn         int         `json:"manager_token_on"`         // 管理员token是否开启
	Conns                  []*ConnInfo `json:"conns,omitempty"`          // 连接信息
	ConversationCacheCount int         `json:"conversation_cache_count"` // 最近会话缓存数量
}

type SystemSetting struct {
	Logger struct {
		TraceOn int `json:"trace_on"` // 日志是否开启trace
		LokiOn  int `json:"loki_on"`  // 日志是否开启loki
	} `json:"logger"`

	PrometheusOn int `json:"prometheus_on"` // 是否开启prometheus
	StressOn     int `json:"stress_on"`     // 是否开启压测
}
