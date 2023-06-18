package server

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
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
	uid := c.Query("uid")

	offset := int(offset64)
	limit := int(limit64)

	if limit <= 0 {
		limit = 20
	}

	sortOpt := ByID
	if sortStr != "" {
		sortOpt = SortOpt(sortStr)
	}
	var connInfos []*ConnInfo
	if strings.TrimSpace(uid) != "" {
		connInfos = make([]*ConnInfo, 0, 4)
		if len(conns) > 0 {
			for _, conn := range conns {
				connInfos = append(connInfos, newConnInfo(conn))
			}
		}

	} else {
		resultConns := co.s.GetConnInfos(sortOpt, offset, limit)
		connInfos = make([]*ConnInfo, 0, len(resultConns))
		for _, resultConn := range resultConns {
			if resultConn == nil || !resultConn.IsAuthed() {
				continue
			}
			connInfos = append(connInfos, newConnInfo(resultConn))
		}
	}

	c.JSON(http.StatusOK, Connz{
		Connections: connInfos,
		Now:         time.Now(),
		Total:       len(conns),
		Offset:      offset,
		Limit:       limit,
	})
}

func (s *Server) GetConnInfos(sortOpt SortOpt, offset, limit int) []wknet.Conn {
	conns := s.dispatch.engine.GetAllConn()
	switch sortOpt {
	case ByID:
		sort.Sort(byID{Conns: conns})
	case ByIDDesc:
		sort.Sort(byIDDesc{Conns: conns})
	case ByInMsg:
		sort.Sort(byInMsg{Conns: conns})
	case ByInMsgDesc:
		sort.Sort(byInMsgDesc{Conns: conns})
	case ByOutMsg:
		sort.Sort(byOutMsg{Conns: conns})
	case ByOutMsgDesc:
		sort.Sort(byOutMsgDesc{Conns: conns})
	case ByInBytes:
		sort.Sort(byInBytes{Conns: conns})
	case ByInBytesDesc:
		sort.Sort(byInBytesDesc{Conns: conns})
	case ByOutBytes:
		sort.Sort(byOutBytes{Conns: conns})
	case ByOutBytesDesc:
		sort.Sort(byOutBytesDesc{Conns: conns})
	case ByPendingBytes:
		sort.Sort(byPendingBytes{Conns: conns})
	case ByPendingBytesDesc:
		sort.Sort(byPendingBytesDesc{Conns: conns})
	case ByUptime:
		sort.Sort(byUptime{Conns: conns})
	case ByUptimeDesc:
		sort.Sort(byUptimeDesc{Conns: conns})
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

	return resultConns
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
	connStats := c.ConnStats()

	return &ConnInfo{
		ID:           c.ID(),
		UID:          c.UID(),
		IP:           host,
		Port:         port,
		LastActivity: c.LastActivity(),
		Uptime:       myUptime(now.Sub(c.Uptime())),
		Idle:         myUptime(now.Sub(c.LastActivity())),
		PendingBytes: c.OutboundBuffer().BoundBufferSize(),
		InMsgs:       connStats.InMsgs.Load(),
		OutMsgs:      connStats.OutMsgs.Load(),
		InBytes:      connStats.InBytes.Load(),
		OutBytes:     connStats.OutBytes.Load(),
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
	ByID     SortOpt = "id"     // 通过连接id排序
	ByIDDesc SortOpt = "idDesc" // 通过连接id排序

	ByInMsg            SortOpt = "inMsg"            // 通过收到消息排序
	ByInMsgDesc        SortOpt = "inMsgDesc"        // 通过收到消息排序
	ByOutMsg           SortOpt = "outMsg"           // 通过发送消息排序
	ByOutMsgDesc       SortOpt = "outMsgDesc"       // 通过发送消息排序
	ByInBytes          SortOpt = "inBytes"          // 通过收到字节数排序
	ByInBytesDesc      SortOpt = "inBytesDesc"      // 通过收到字节数排序
	ByOutBytes         SortOpt = "outBytes"         // 通过发送字节数排序
	ByOutBytesDesc     SortOpt = "outBytesDesc"     // 通过发送字节数排序
	ByPendingBytes     SortOpt = "pendingBytes"     // 通过等待发送字节数排序
	ByPendingBytesDesc SortOpt = "pendingBytesDesc" // 通过等待发送字节数排序
	ByUptime           SortOpt = "uptime"           // 通过启动时间排序
	ByUptimeDesc       SortOpt = "uptimeDesc"       // 通过启动时间排序
)

// byID
type byID struct{ Conns []wknet.Conn }

func (l byID) Less(i, j int) bool {
	return l.Conns[i].ID() < l.Conns[j].ID()
}

func (l byID) Len() int {
	return len(l.Conns)
}
func (l byID) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// byIDDesc
type byIDDesc struct{ Conns []wknet.Conn }

func (l byIDDesc) Less(i, j int) bool {
	return l.Conns[i].ID() > l.Conns[j].ID()
}

func (l byIDDesc) Len() int {
	return len(l.Conns)
}
func (l byIDDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// inMsg
type byInMsg struct{ Conns []wknet.Conn }

func (l byInMsg) Less(i, j int) bool {
	return l.Conns[i].ConnStats().InMsgs.Load() < l.Conns[j].ConnStats().InMsgs.Load()
}
func (l byInMsg) Len() int      { return len(l.Conns) }
func (l byInMsg) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byInMsgDesc struct{ Conns []wknet.Conn }

func (l byInMsgDesc) Less(i, j int) bool {
	return l.Conns[i].ConnStats().InMsgs.Load() > l.Conns[j].ConnStats().InMsgs.Load()
}

func (l byInMsgDesc) Len() int      { return len(l.Conns) }
func (l byInMsgDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// byOutMsg

type byOutMsg struct{ Conns []wknet.Conn }

func (l byOutMsg) Less(i, j int) bool {
	return l.Conns[i].ConnStats().OutMsgs.Load() < l.Conns[j].ConnStats().OutMsgs.Load()
}
func (l byOutMsg) Len() int      { return len(l.Conns) }
func (l byOutMsg) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byOutMsgDesc struct{ Conns []wknet.Conn }

func (l byOutMsgDesc) Less(i, j int) bool {
	return l.Conns[i].ConnStats().OutMsgs.Load() > l.Conns[j].ConnStats().OutMsgs.Load()
}
func (l byOutMsgDesc) Len() int      { return len(l.Conns) }
func (l byOutMsgDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// byInBytes

type byInBytes struct{ Conns []wknet.Conn }

func (l byInBytes) Less(i, j int) bool {
	return l.Conns[i].ConnStats().InBytes.Load() < l.Conns[j].ConnStats().InBytes.Load()
}
func (l byInBytes) Len() int      { return len(l.Conns) }
func (l byInBytes) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byInBytesDesc struct{ Conns []wknet.Conn }

func (l byInBytesDesc) Less(i, j int) bool {
	return l.Conns[i].ConnStats().InBytes.Load() > l.Conns[j].ConnStats().InBytes.Load()
}
func (l byInBytesDesc) Len() int      { return len(l.Conns) }
func (l byInBytesDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// outBytes

type byOutBytes struct{ Conns []wknet.Conn }

func (l byOutBytes) Less(i, j int) bool {
	return l.Conns[i].ConnStats().OutBytes.Load() < l.Conns[j].ConnStats().OutBytes.Load()
}
func (l byOutBytes) Len() int      { return len(l.Conns) }
func (l byOutBytes) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byOutBytesDesc struct{ Conns []wknet.Conn }

func (l byOutBytesDesc) Less(i, j int) bool {
	return l.Conns[i].ConnStats().OutBytes.Load() > l.Conns[j].ConnStats().OutBytes.Load()
}
func (l byOutBytesDesc) Len() int      { return len(l.Conns) }
func (l byOutBytesDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// PendingBytes

type byPendingBytes struct{ Conns []wknet.Conn }

func (l byPendingBytes) Less(i, j int) bool {
	return l.Conns[i].OutboundBuffer().BoundBufferSize() < l.Conns[j].OutboundBuffer().BoundBufferSize()
}
func (l byPendingBytes) Len() int      { return len(l.Conns) }
func (l byPendingBytes) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byPendingBytesDesc struct{ Conns []wknet.Conn }

func (l byPendingBytesDesc) Less(i, j int) bool {
	return l.Conns[i].OutboundBuffer().BoundBufferSize() > l.Conns[j].OutboundBuffer().BoundBufferSize()
}
func (l byPendingBytesDesc) Len() int      { return len(l.Conns) }
func (l byPendingBytesDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// uptime

type byUptime struct{ Conns []wknet.Conn }

func (l byUptime) Less(i, j int) bool {
	return l.Conns[i].Uptime().Before(l.Conns[j].Uptime())
}
func (l byUptime) Len() int      { return len(l.Conns) }
func (l byUptime) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byUptimeDesc struct{ Conns []wknet.Conn }

func (l byUptimeDesc) Less(i, j int) bool {
	return l.Conns[i].Uptime().After(l.Conns[j].Uptime())
}
func (l byUptimeDesc) Len() int      { return len(l.Conns) }
func (l byUptimeDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }
