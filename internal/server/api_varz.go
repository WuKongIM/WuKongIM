package server

import (
	"fmt"
	"math"
	"net/http"
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
}

func (v *VarzAPI) HandleVarz(c *wkhttp.Context) {

	show := c.Query("show")
	connLimit, _ := strconv.Atoi(c.Query("conn_limit"))

	if connLimit == 0 {
		connLimit = 20
	}
	varz := CreateVarz(v.s)

	if show == "conn" {
		resultConns := v.s.GetConnInfos(ByInMsgDesc, 0, connLimit)
		connInfos := make([]*ConnInfo, 0, len(resultConns))
		for _, resultConn := range resultConns {
			if resultConn == nil || !resultConn.IsAuthed() {
				continue
			}
			connInfos = append(connInfos, newConnInfo(resultConn))
		}
		varz.Conns = connInfos
	}

	c.JSON(http.StatusOK, varz)
}

func CreateVarz(s *Server) *Varz {
	var rss, vss int64 // rss内存 vss虚拟内存
	var pcpu float64   // cpu
	err := pse.ProcUsage(&pcpu, &rss, &vss)
	if err != nil {
		s.Error("获取系统资源失败", zap.Error(err))
	}
	opts := s.opts
	connCount := s.dispatch.engine.ConnCount()
	s.retryQueue.inFlightMutex.Lock()
	retryQueueF := math.Max(float64(len(s.retryQueue.inFlightMessages)), float64(len(s.retryQueue.inFlightPQ)))
	s.retryQueue.inFlightMutex.Unlock()
	return &Varz{
		ServerID:    fmt.Sprintf("%d", opts.ID),
		ServerName:  "WuKongIM",
		Version:     version.Version,
		Connections: connCount,
		Uptime:      myUptime(time.Since(s.start)),
		CPU:         pcpu,
		Mem:         rss,
		InMsgs:      s.inMsgs.Load(),
		OutMsgs:     s.outMsgs.Load(),
		InBytes:     s.inBytes.Load(),
		OutBytes:    s.outBytes.Load(),
		SlowClients: s.slowClients.Load(),
		RetryQueue:  int64(retryQueueF),

		TCPAddr:        opts.External.TCPAddr,
		WSAddr:         opts.External.WSAddr,
		WSSAddr:        opts.External.WSSAddr,
		MonitorAddr:    opts.External.MonitorAddr,
		APIURL:         opts.External.APIUrl,
		MonitorOn:      wkutil.BoolToInt(opts.Monitor.On),
		Commit:         version.Commit,
		CommitDate:     version.CommitDate,
		TreeState:      version.TreeState,
		ManagerUID:     opts.ManagerUID,
		ManagerTokenOn: wkutil.BoolToInt(opts.ManagerTokenOn),
	}
}

type Varz struct {
	ServerID    string  `json:"server_id"`   // 服务端ID
	ServerName  string  `json:"server_name"` // 服务端名称
	Version     string  `json:"version"`     // 服务端版本
	Connections int     `json:"connections"` // 当前连接数量
	Uptime      string  `json:"uptime"`      // 上线时间
	Mem         int64   `json:"mem"`         // 内存
	CPU         float64 `json:"cpu"`         // cpu

	InMsgs      int64 `json:"in_msgs"`      // 流入消息数量
	OutMsgs     int64 `json:"out_msgs"`     // 流出消息数量
	InBytes     int64 `json:"in_bytes"`     // 流入字节数量
	OutBytes    int64 `json:"out_bytes"`    // 流出字节数量
	SlowClients int64 `json:"slow_clients"` // 慢客户端数量
	RetryQueue  int64 `json:"retry_queue"`  // 重试队列数量

	TCPAddr     string `json:"tcp_addr"`     // tcp地址
	WSAddr      string `json:"ws_addr"`      // ws地址
	WSSAddr     string `json:"wss_addr"`     // wss地址
	MonitorAddr string `json:"monitor_addr"` // 监控地址
	MonitorOn   int    `json:"monitor_on"`   // 监控是否开启
	Commit      string `json:"commit"`       // git commit id
	CommitDate  string `json:"commit_date"`  // git commit date
	TreeState   string `json:"tree_state"`   // git tree state
	APIURL      string `json:"api_url"`      // api地址

	ManagerUID     string `json:"manager_uid"`      // 管理员uid
	ManagerTokenOn int    `json:"manager_token_on"` // 管理员token是否开启

	Conns []*ConnInfo `json:"conns,omitempty"` // 连接信息

}
