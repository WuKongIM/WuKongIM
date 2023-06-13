package server

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
)

type ConnzAPI struct {
	wklog.Log
	s *Server
}

func NewConnzAPI(s *Server) *ConnzAPI {
	return &ConnzAPI{
		Log: wklog.NewWKLog("ConnzAPI"),
		s:   s,
	}
}

func (co *ConnzAPI) Route(r *wkhttp.WKHttp) {
	r.GET("/connz", co.HandleConnz)
}

func (co *ConnzAPI) HandleConnz(c *wkhttp.Context) {
	conns := co.s.dispatch.engine.GetAllConn()
	sortStr := c.Query("sort")
	offset64, _ := strconv.ParseInt(c.Query("offset"), 10, 64)
	limit64, _ := strconv.ParseInt(c.Query("limit"), 10, 64)

	offset := int(offset64)
	limit := int(limit64)

	if limit <= 0 {
		limit = 20
	}

	sortOpt := ByID
	if sortStr != "" {
		sortOpt = SortOpt(sortOpt)
	}

	switch sortOpt {
	case ByID:
		sort.Sort(byID{Conns: conns})
	}

	minoff := offset
	maxoff := offset + limit
	maxIndex := len(conns)

	if minoff > maxIndex {
		minoff = maxIndex
	}
	if maxoff > maxIndex {
		maxoff = maxIndex
	}

	resultConns := conns[minoff:maxoff]

	connInfos := make([]*ConnInfo, 0, len(resultConns))

	for _, resultConn := range resultConns {
		if resultConn == nil || !resultConn.IsAuthed() {
			continue
		}
		connInfos = append(connInfos, newConnInfo(resultConn))
	}

	c.JSON(http.StatusOK, Connz{
		Connections: connInfos,
		Now:         time.Now(),
		Total:       len(conns),
		Offset:      offset,
		Limit:       limit,
	})
}

type Connz struct {
	Connections []*ConnInfo `json:"connections"` // 连接数
	Now         time.Time   `json:"now"`         // 查询时间
	Total       int         `json:"total"`       // 总连接数量
	Offset      int         `json:"offset"`      // 偏移位置
	Limit       int         `json:"limit"`       // 限制数量
}

type ConnInfo struct {
	ID           int64     `json:"id"`            // 连接ID
	UID          string    `json:"uid"`           // 用户uid
	IP           string    `json:"ip"`            // 客户端IP
	Port         int       `json:"port"`          // 客户端端口
	LastActivity time.Time `json:"last_activity"` // 最后一次活动时间
	Uptime       string    `json:"uptime"`        // 启动时间
	Idle         string    `json:"idle"`          // 客户端闲置时间
	PendingBytes int       `json:"pending_bytes"` // 等待发送的字节数
	InMsgs       int64     `json:"in_msgs"`       // 流入的消息数
	OutMsgs      int64     `json:"out_msgs"`      // 流出的消息数量
	InBytes      int64     `json:"in_bytes"`      // 流入的字节数量
	OutBytes     int64     `json:"out_bytes"`     // 流出的字节数量
	Device       string    `json:"device"`        // 设备
	DeviceID     string    `json:"device_id"`     // 设备ID
	Version      uint8     `json:"version"`       // 客户端协议版本
}

func newConnInfo(c wknet.Conn) *ConnInfo {
	var (
		now  = time.Now()
		host string
		port int
	)

	if c.RemoteAddr() != nil {
		hostStr, portStr, _ := net.SplitHostPort(c.RemoteAddr().String())
		port, _ = strconv.Atoi(portStr)
		host = hostStr
	}
	connCtx := c.Context().(*connContext)

	return &ConnInfo{
		ID:           c.ID(),
		UID:          c.UID(),
		IP:           host,
		Port:         port,
		LastActivity: c.LastActivity(),
		Uptime:       myUptime(now.Sub(c.Uptime())),
		Idle:         myUptime(now.Sub(c.LastActivity())),
		PendingBytes: c.OutboundBuffer().BoundBufferSize(),
		InMsgs:       connCtx.inMsgs.Load(),
		OutMsgs:      connCtx.outMsgs.Load(),
		InBytes:      connCtx.inBytes.Load(),
		OutBytes:     connCtx.outBytes.Load(),
		Device:       device(c),
		DeviceID:     c.DeviceID(),
		Version:      uint8(c.ProtoVersion()),
	}
}

func device(conn wknet.Conn) string {
	d := "未知"
	level := "主"
	switch wkproto.DeviceFlag(conn.DeviceFlag()) {
	case wkproto.APP:
		d = "App"
	case wkproto.PC:
		d = "PC"
	case wkproto.WEB:
		d = "Web"
	}

	if wkproto.DeviceLevel(conn.DeviceLevel()) == wkproto.DeviceLevelSlave {
		level = "从"
	}

	return fmt.Sprintf("%s(%s)", d, level)
}

type SortOpt string

const (
	ByID SortOpt = "id" // 通过连接id排序
)

type byID struct{ Conns []wknet.Conn }

func (l byID) Less(i, j int) bool { return l.Conns[i].ID() < l.Conns[j].ID() }

func (l byID) Len() int      { return len(l.Conns) }
func (l byID) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }
