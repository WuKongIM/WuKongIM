package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/pse"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/WuKongIM/WuKongIM/version"
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
	r.GET("/varz", v.HandleVarz)
}

func (v *VarzAPI) HandleVarz(c *wkhttp.Context) {

	var rss, vss int64 // rss内存 vss虚拟内存
	var pcpu float64   // cpu

	// We want to do that outside of the lock.
	pse.ProcUsage(&pcpu, &rss, &vss)

	varz := v.createVarz(pcpu, rss)

	c.JSON(http.StatusOK, varz)
}

func (v *VarzAPI) createVarz(pcpu float64, rss int64) *Varz {
	s := v.s
	opts := s.opts
	connCount := v.s.dispatch.engine.ConnCount()
	return &Varz{
		ServerID:      fmt.Sprintf("%d", opts.ID),
		ServerName:    "WuKongIM",
		Version:       version.Version,
		Connections:   connCount,
		Uptime:        myUptime(time.Since(v.s.start)),
		CPU:           pcpu,
		Mem:           rss,
		InMsgs:        s.inMsgs.Load(),
		OutMsgs:       s.outMsgs.Load(),
		InBytes:       s.inBytes.Load(),
		OutBytes:      s.outBytes.Load(),
		SlowConsumers: s.slowClients.Load(),

		TCPAddr:     opts.External.TCPAddr,
		WSAddr:      opts.External.WSSAddr,
		MonitorAddr: opts.External.MonitorAddr,
		MonitorOn:   wkutil.BoolToInt(opts.Monitor.On),
		Commit:      version.Commit,
		CommitDate:  version.CommitDate,
		TreeState:   version.TreeState,
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

	InMsgs        int64 `json:"in_msgs"`        // 流入消息数量
	OutMsgs       int64 `json:"out_msgs"`       // 流出消息数量
	InBytes       int64 `json:"in_bytes"`       // 流入字节数量
	OutBytes      int64 `json:"out_bytes"`      // 流出字节数量
	SlowConsumers int64 `json:"slow_consumers"` // 慢客户端数量

	//
	TCPAddr     string `json:"tcp_addr"`     // tcp地址
	WSAddr      string `json:"ws_addr"`      // ws地址
	MonitorAddr string `json:"monitor_addr"` // 监控地址
	MonitorOn   int    `json:"monitor_on"`   // 监控是否开启
	Commit      string `json:"commit"`       // git commit id
	CommitDate  string `json:"commit_date"`  // git commit date
	TreeState   string `json:"tree_state"`   // git tree state

}
