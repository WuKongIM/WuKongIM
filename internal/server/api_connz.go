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
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
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

	sortStr := c.Query("sort")
	offset64, _ := strconv.ParseInt(c.Query("offset"), 10, 64)
	limit64, _ := strconv.ParseInt(c.Query("limit"), 10, 64)
	uid := c.Query("uid")

	nodeIdStr := c.Query("node_id")
	var nodeId uint64
	if strings.TrimSpace(nodeIdStr) != "" {
		nodeId, _ = strconv.ParseUint(nodeIdStr, 10, 64)
	}

	if nodeId > 0 && nodeId != co.s.opts.Cluster.NodeId {
		nodeInfo, err := co.s.cluster.NodeInfoById(nodeId)
		if err != nil {
			co.Error("获取节点信息失败！", zap.Error(err), zap.Uint64("nodeId", nodeId))
			c.ResponseError(err)
			return
		}
		if nodeInfo == nil {
			co.Error("节点不存在！", zap.Uint64("nodeId", nodeId))
			c.ResponseError(fmt.Errorf("节点不存在！"))
			return
		}
		c.ForwardWithBody(fmt.Sprintf("%s%s", nodeInfo.ApiServerAddr, c.Request.URL.Path), nil)
		return
	}

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
	resultConns := co.s.GetConnInfos(uid, sortOpt, offset, limit)
	connInfos = make([]*ConnInfo, 0, len(resultConns))
	for _, resultConn := range resultConns {
		if resultConn == nil || !resultConn.IsAuthed() {
			continue
		}
		connInfos = append(connInfos, newConnInfo(resultConn))
	}

	c.JSON(http.StatusOK, Connz{
		Connections: connInfos,
		Now:         time.Now(),
		Total:       co.s.dispatch.engine.ConnCount(),
		Offset:      offset,
		Limit:       limit,
	})
}

func (s *Server) GetConnInfos(uid string, sortOpt SortOpt, offset, limit int) []wknet.Conn {
	conns := s.dispatch.engine.GetAllConn()
	if strings.TrimSpace(uid) != "" {
		uidConns := make([]wknet.Conn, 0, 10)
		for _, conn := range conns {
			if conn.UID() == uid {
				uidConns = append(uidConns, conn)
			}
		}
		conns = uidConns
	}

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
	case ByInMsgBytes:
		sort.Sort(byInMsgBytes{Conns: conns})
	case ByInMsgBytesDesc:
		sort.Sort(byInMsgBytesDesc{Conns: conns})
	case ByOutBytes:
		sort.Sort(byOutMsgBytes{Conns: conns})
	case ByOutBytesDesc:
		sort.Sort(byOutMsgBytesDesc{Conns: conns})
	case ByOutPacket:
		sort.Sort(byOutPacket{Conns: conns})
	case ByOutPacketDesc:
		sort.Sort(byOutPacketDesc{Conns: conns})
	case ByInPacket:
		sort.Sort(byInPacket{Conns: conns})
	case ByInPacketDesc:
		sort.Sort(byInPacketDesc{Conns: conns})
	case ByOutPacketBytes:
		sort.Sort(byOutPacketBytes{Conns: conns})
	case ByOutPacketBytesDesc:
		sort.Sort(byOutPacketBytesDesc{Conns: conns})
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
	ID             int64     `json:"id"`               // 连接ID
	UID            string    `json:"uid"`              // 用户uid
	IP             string    `json:"ip"`               // 客户端IP
	Port           int       `json:"port"`             // 客户端端口
	LastActivity   time.Time `json:"last_activity"`    // 最后一次活动时间
	Uptime         string    `json:"uptime"`           // 启动时间
	Idle           string    `json:"idle"`             // 客户端闲置时间
	PendingBytes   int       `json:"pending_bytes"`    // 等待发送的字节数
	InMsgs         int64     `json:"in_msgs"`          // 流入的消息数
	OutMsgs        int64     `json:"out_msgs"`         // 流出的消息数量
	InMsgBytes     int64     `json:"in_msg_bytes"`     // 流入的消息字节数量
	OutMsgBytes    int64     `json:"out_msg_bytes"`    // 流出的消息字节数量
	InPackets      int64     `json:"in_packets"`       // 流入的包数量
	OutPackets     int64     `json:"out_packets"`      // 流出的包数量
	InPacketBytes  int64     `json:"in_packet_bytes"`  // 流入的包字节数量
	OutPacketBytes int64     `json:"out_packet_bytes"` // 流出的包字节数量
	Device         string    `json:"device"`           // 设备
	DeviceID       string    `json:"device_id"`        // 设备ID
	Version        uint8     `json:"version"`          // 客户端协议版本
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
		ID:             c.ID(),
		UID:            c.UID(),
		IP:             host,
		Port:           port,
		LastActivity:   c.LastActivity(),
		Uptime:         myUptime(now.Sub(c.Uptime())),
		Idle:           myUptime(now.Sub(c.LastActivity())),
		PendingBytes:   c.OutboundBuffer().BoundBufferSize(),
		InMsgs:         connStats.InMsgs.Load(),
		OutMsgs:        connStats.OutMsgs.Load(),
		InMsgBytes:     connStats.InMsgBytes.Load(),
		OutMsgBytes:    connStats.OutMsgBytes.Load(),
		InPackets:      connStats.InPackets.Load(),
		OutPackets:     connStats.OutPackets.Load(),
		InPacketBytes:  connStats.InPacketBytes.Load(),
		OutPacketBytes: connStats.OutPacketBytes.Load(),
		Device:         device(c),
		DeviceID:       c.DeviceID(),
		Version:        uint8(c.ProtoVersion()),
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

	ByInMsg              SortOpt = "inMsg"              // 通过收到消息排序
	ByInMsgDesc          SortOpt = "inMsgDesc"          // 通过收到消息排序
	ByOutMsg             SortOpt = "outMsg"             // 通过发送消息排序
	ByOutMsgDesc         SortOpt = "outMsgDesc"         // 通过发送消息排序
	ByInMsgBytes         SortOpt = "inMsgBytes"         // 通过收到字节数排序
	ByInMsgBytesDesc     SortOpt = "inMsgBytesDesc"     // 通过收到字节数排序
	ByOutBytes           SortOpt = "outBytes"           // 通过发送字节数排序
	ByOutBytesDesc       SortOpt = "outBytesDesc"       // 通过发送字节数排序
	ByPendingBytes       SortOpt = "pendingBytes"       // 通过等待发送字节数排序
	ByPendingBytesDesc   SortOpt = "pendingBytesDesc"   // 通过等待发送字节数排序
	ByUptime             SortOpt = "uptime"             // 通过启动时间排序
	ByUptimeDesc         SortOpt = "uptimeDesc"         // 通过启动时间排序
	ByOutPacket          SortOpt = "outPacket"          // 通过发送包排序
	ByOutPacketDesc      SortOpt = "outPacketDesc"      // 通过发送包排序
	ByInPacket           SortOpt = "inPacket"           // 通过接收包排序
	ByInPacketDesc       SortOpt = "inPacketDesc"       // 通过接收包排序
	ByInPacketBytes      SortOpt = "inPacketBytes"      // 通过接收包字节数排序
	ByInPacketBytesDesc  SortOpt = "inPacketBytesDesc"  // 通过接收包字节数排序
	ByOutPacketBytes     SortOpt = "outPacketBytes"     // 通过发送包字节数排序
	ByOutPacketBytesDesc SortOpt = "outPacketBytesDesc" // 通过发送包字节数排序

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

// byInMsgBytes

type byInMsgBytes struct{ Conns []wknet.Conn }

func (l byInMsgBytes) Less(i, j int) bool {
	return l.Conns[i].ConnStats().InMsgBytes.Load() < l.Conns[j].ConnStats().InMsgBytes.Load()
}
func (l byInMsgBytes) Len() int      { return len(l.Conns) }
func (l byInMsgBytes) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byInMsgBytesDesc struct{ Conns []wknet.Conn }

func (l byInMsgBytesDesc) Less(i, j int) bool {
	return l.Conns[i].ConnStats().InMsgBytes.Load() > l.Conns[j].ConnStats().InMsgBytes.Load()
}
func (l byInMsgBytesDesc) Len() int      { return len(l.Conns) }
func (l byInMsgBytesDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// outMsgBytes

type byOutMsgBytes struct{ Conns []wknet.Conn }

func (l byOutMsgBytes) Less(i, j int) bool {
	return l.Conns[i].ConnStats().OutMsgBytes.Load() < l.Conns[j].ConnStats().OutMsgBytes.Load()
}
func (l byOutMsgBytes) Len() int      { return len(l.Conns) }
func (l byOutMsgBytes) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byOutMsgBytesDesc struct{ Conns []wknet.Conn }

func (l byOutMsgBytesDesc) Less(i, j int) bool {
	return l.Conns[i].ConnStats().OutMsgBytes.Load() > l.Conns[j].ConnStats().OutMsgBytes.Load()
}
func (l byOutMsgBytesDesc) Len() int      { return len(l.Conns) }
func (l byOutMsgBytesDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// outPacketBytes

type byOutPacketBytes struct{ Conns []wknet.Conn }

func (l byOutPacketBytes) Less(i, j int) bool {
	return l.Conns[i].ConnStats().OutPacketBytes.Load() < l.Conns[j].ConnStats().OutPacketBytes.Load()
}

func (l byOutPacketBytes) Len() int      { return len(l.Conns) }
func (l byOutPacketBytes) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byOutPacketBytesDesc struct{ Conns []wknet.Conn }

func (l byOutPacketBytesDesc) Less(i, j int) bool {
	return l.Conns[i].ConnStats().OutPacketBytes.Load() > l.Conns[j].ConnStats().OutPacketBytes.Load()
}

func (l byOutPacketBytesDesc) Len() int { return len(l.Conns) }

func (l byOutPacketBytesDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// inPacketBytes

type byInPacketBytes struct{ Conns []wknet.Conn }

func (l byInPacketBytes) Less(i, j int) bool {
	return l.Conns[i].ConnStats().InPacketBytes.Load() < l.Conns[j].ConnStats().InPacketBytes.Load()
}

func (l byInPacketBytes) Len() int      { return len(l.Conns) }
func (l byInPacketBytes) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byInPacketBytesDesc struct{ Conns []wknet.Conn }

func (l byInPacketBytesDesc) Less(i, j int) bool {
	return l.Conns[i].ConnStats().InPacketBytes.Load() > l.Conns[j].ConnStats().InPacketBytes.Load()
}

func (l byInPacketBytesDesc) Len() int { return len(l.Conns) }

func (l byInPacketBytesDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// outPacket

type byOutPacket struct{ Conns []wknet.Conn }

func (l byOutPacket) Less(i, j int) bool {
	return l.Conns[i].ConnStats().OutPackets.Load() < l.Conns[j].ConnStats().OutPackets.Load()
}

func (l byOutPacket) Len() int { return len(l.Conns) }

func (l byOutPacket) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byOutPacketDesc struct{ Conns []wknet.Conn }

func (l byOutPacketDesc) Less(i, j int) bool {
	return l.Conns[i].ConnStats().OutPackets.Load() > l.Conns[j].ConnStats().OutPackets.Load()
}

func (l byOutPacketDesc) Len() int { return len(l.Conns) }

func (l byOutPacketDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// inPacket

type byInPacket struct{ Conns []wknet.Conn }

func (l byInPacket) Less(i, j int) bool {
	return l.Conns[i].ConnStats().InPackets.Load() < l.Conns[j].ConnStats().InPackets.Load()
}

func (l byInPacket) Len() int { return len(l.Conns) }

func (l byInPacket) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byInPacketDesc struct{ Conns []wknet.Conn }

func (l byInPacketDesc) Less(i, j int) bool {
	return l.Conns[i].ConnStats().InPackets.Load() > l.Conns[j].ConnStats().InPackets.Load()
}

func (l byInPacketDesc) Len() int { return len(l.Conns) }

func (l byInPacketDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

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
