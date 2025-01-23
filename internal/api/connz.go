package api

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

type connz struct {
	wklog.Log
	s *Server
}

func newConnz(s *Server) *connz {
	return &connz{
		Log: wklog.NewWKLog("ConnzAPI"),
		s:   s,
	}
}

func (co *connz) route(r *wkhttp.WKHttp) {
	r.GET("/connz", co.HandleConnz)
}

func (co *connz) HandleConnz(c *wkhttp.Context) {

	sortStr := c.Query("sort")
	offset64, _ := strconv.ParseInt(c.Query("offset"), 10, 64)
	limit64, _ := strconv.ParseInt(c.Query("limit"), 10, 64)
	uid := c.Query("uid")

	nodeIdStr := c.Query("node_id")
	var nodeId uint64
	if strings.TrimSpace(nodeIdStr) != "" {
		nodeId, _ = strconv.ParseUint(nodeIdStr, 10, 64)
	}

	if nodeId > 0 && nodeId != options.G.Cluster.NodeId {
		nodeInfo := service.Cluster.NodeInfoById(nodeId)
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

		if options.G.IsLocalNode(resultConn.NodeId) {
			conn := service.ConnManager.GetConn(resultConn.ConnId)
			if conn != nil {
				resultConn.remoteAddr = conn.RemoteAddr().String()
			}
		} else {
			node := service.Cluster.NodeInfoById(resultConn.NodeId)
			if node != nil {
				resultConn.remoteAddr = node.ClusterAddr

			}
		}

		connInfo := newConnInfo(resultConn)

		proxyType := "未知"
		leaderId := co.slotLeader(resultConn.Uid)
		connInfo.LeaderId = leaderId
		if options.G.IsLocalNode(resultConn.NodeId) {
			proxyType = "本地连接"
		} else {
			proxyType = fmt.Sprintf("源节点：%d", resultConn.NodeId)
		}
		connInfo.ProxyTypeFormat = proxyType

		connInfos = append(connInfos, connInfo)
	}

	c.JSON(http.StatusOK, Connz{
		Connections: connInfos,
		Now:         time.Now(),
		Total:       service.ConnManager.ConnCount(),
		Offset:      offset,
		Limit:       limit,
	})
}

func (co *connz) slotLeader(uid string) uint64 {
	slotId := service.Cluster.GetSlotId(uid)
	leaderId := service.Cluster.SlotLeaderId(slotId)
	return leaderId
}

type connzResp struct {
	*eventbus.Conn
	remoteAddr string
}

func (s *Server) GetConnInfos(uid string, sortOpt SortOpt, offset, limit int) []*connzResp {
	connCtxs := make([]*connzResp, 0, service.ConnManager.ConnCount())
	conns := eventbus.User.AllConn()
	for _, conn := range conns {
		connCtx := &connzResp{
			Conn: conn,
			// remoteAddr:   conn.RemoteAddr().String(),
			// lastActivity: conn.LastActivity(),
		}
		if strings.TrimSpace(uid) != "" {
			if strings.Contains(connCtx.Uid, uid) {
				connCtxs = append(connCtxs, connCtx)
			}
		} else {
			connCtxs = append(connCtxs, connCtx)
		}
	}

	switch sortOpt {
	case ByID:
		sort.Sort(byID{Conns: connCtxs})
	case ByIDDesc:
		sort.Sort(byIDDesc{Conns: connCtxs})
	case ByInMsg:
		sort.Sort(byInMsg{Conns: connCtxs})
	case ByInMsgDesc:
		sort.Sort(byInMsgDesc{Conns: connCtxs})
	case ByOutMsg:
		sort.Sort(byOutMsg{Conns: connCtxs})
	case ByOutMsgDesc:
		sort.Sort(byOutMsgDesc{Conns: connCtxs})
	case ByInMsgBytes:
		sort.Sort(byInMsgBytes{Conns: connCtxs})
	case ByInMsgBytesDesc:
		sort.Sort(byInMsgBytesDesc{Conns: connCtxs})
	case ByOutMsgBytes:
		sort.Sort(byOutMsgBytes{Conns: connCtxs})
	case ByOutMsgBytesDesc:
		sort.Sort(byOutMsgBytesDesc{Conns: connCtxs})
	case ByOutPacket:
		sort.Sort(byOutPacket{Conns: connCtxs})
	case ByOutPacketDesc:
		sort.Sort(byOutPacketDesc{Conns: connCtxs})
	case ByInPacket:
		sort.Sort(byInPacket{Conns: connCtxs})
	case ByInPacketDesc:
		sort.Sort(byInPacketDesc{Conns: connCtxs})
	case ByInPacketBytes:
		sort.Sort(byInPacketBytes{Conns: connCtxs})
	case ByInPacketBytesDesc:
		sort.Sort(byInPacketBytesDesc{Conns: connCtxs})
	case ByOutPacketBytes:
		sort.Sort(byOutPacketBytes{Conns: connCtxs})
	case ByOutPacketBytesDesc:
		sort.Sort(byOutPacketBytesDesc{Conns: connCtxs})
	// case ByPendingBytes:
	// sort.Sort(byPendingBytes{Conns: connCtxs})
	// case ByPendingBytesDesc:
	// sort.Sort(byPendingBytesDesc{Conns: connCtxs})
	case ByUptime:
		sort.Sort(byUptime{Conns: connCtxs})
	case ByUptimeDesc:
		sort.Sort(byUptimeDesc{Conns: connCtxs})
	case ByIdle:
		sort.Sort(byIdle{Conns: connCtxs})
	case ByIdleDesc:
		sort.Sort(byIdleDesc{Conns: connCtxs})
	case ByProtoVersion:
		sort.Sort(byProtoVersion{Conns: connCtxs})
	case ByProtoVersionDesc:
		sort.Sort(byProtoVersionDesc{Conns: connCtxs})

	}

	minoff := offset
	maxoff := offset + limit
	maxIndex := len(connCtxs)

	if minoff > maxIndex {
		minoff = maxIndex
	}
	if maxoff > maxIndex {
		maxoff = maxIndex
	}

	resultConns := connCtxs[minoff:maxoff]

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
	ID              int64     `json:"id"`                // 连接ID
	UID             string    `json:"uid"`               // 用户uid
	IP              string    `json:"ip"`                // 客户端IP
	Port            int       `json:"port"`              // 客户端端口
	LastActivity    time.Time `json:"last_activity"`     // 最后一次活动时间
	Uptime          string    `json:"uptime"`            // 启动时间
	Idle            string    `json:"idle"`              // 客户端闲置时间
	PendingBytes    int       `json:"pending_bytes"`     // 等待发送的字节数
	InMsgs          int64     `json:"in_msgs"`           // 流入的消息数
	OutMsgs         int64     `json:"out_msgs"`          // 流出的消息数量
	InMsgBytes      int64     `json:"in_msg_bytes"`      // 流入的消息字节数量
	OutMsgBytes     int64     `json:"out_msg_bytes"`     // 流出的消息字节数量
	InPackets       int64     `json:"in_packets"`        // 流入的包数量
	OutPackets      int64     `json:"out_packets"`       // 流出的包数量
	InPacketBytes   int64     `json:"in_packet_bytes"`   // 流入的包字节数量
	OutPacketBytes  int64     `json:"out_packet_bytes"`  // 流出的包字节数量
	Device          string    `json:"device"`            // 设备
	DeviceID        string    `json:"device_id"`         // 设备ID
	Version         uint8     `json:"version"`           // 客户端协议版本
	ProxyTypeFormat string    `json:"proxy_type_format"` // 代理类型
	LeaderId        uint64    `json:"leader_id"`         // 领导节点id
	NodeId          uint64    `json:"node_id"`           // 连接源节点
}

func newConnInfo(connCtx *connzResp) *ConnInfo {
	var (
		now  = time.Now()
		host string
		port int
	)
	if connCtx.remoteAddr != "" {
		hostStr, portStr, _ := net.SplitHostPort(connCtx.remoteAddr)
		port, _ = strconv.Atoi(portStr)
		host = hostStr
	}

	lastActivity := time.Unix(int64(connCtx.LastActive), 0)

	uptime := time.Unix(int64(connCtx.Uptime), 0)

	return &ConnInfo{
		ID:           connCtx.ConnId,
		UID:          connCtx.Uid,
		IP:           host,
		Port:         port,
		LastActivity: lastActivity,
		Uptime:       myUptime(now.Sub(uptime)),
		Idle:         myUptime(now.Sub(lastActivity)),
		// PendingBytes:   c.OutboundBuffer().BoundBufferSize(),
		InMsgs:         connCtx.InMsgCount.Load(),
		OutMsgs:        connCtx.OutMsgCount.Load(),
		InMsgBytes:     connCtx.InMsgByteCount.Load(),
		OutMsgBytes:    connCtx.OutMsgByteCount.Load(),
		InPackets:      connCtx.InPacketCount.Load(),
		OutPackets:     connCtx.OutPacketCount.Load(),
		InPacketBytes:  connCtx.InPacketByteCount.Load(),
		OutPacketBytes: connCtx.OutPacketByteCount.Load(),
		Device:         device(connCtx),
		DeviceID:       connCtx.DeviceId,
		Version:        connCtx.ProtoVersion,
		NodeId:         connCtx.NodeId,
	}
}

func device(connCtx *connzResp) string {
	d := "未知"
	level := "主"
	switch wkproto.DeviceFlag(connCtx.DeviceFlag) {
	case wkproto.APP:
		d = "App"
	case wkproto.PC:
		d = "PC"
	case wkproto.WEB:
		d = "Web"
	}

	if wkproto.DeviceLevel(connCtx.DeviceLevel) == wkproto.DeviceLevelSlave {
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
	ByOutMsgBytes        SortOpt = "outMsgBytes"        // 通过发送字节数排序
	ByOutMsgBytesDesc    SortOpt = "outMsgBytesDesc"    // 通过发送字节数排序
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
	ByIdle               SortOpt = "idle"               // 通过闲置时间排序
	ByIdleDesc           SortOpt = "idleDesc"           // 通过闲置时间排序
	ByProtoVersion       SortOpt = "protoVersion"       // 通过协议版本排序
	ByProtoVersionDesc   SortOpt = "protoVersionDesc"   // 通过协议版本排序

)

// byID
type byID struct{ Conns []*connzResp }

func (l byID) Less(i, j int) bool {
	return l.Conns[i].ConnId < l.Conns[j].ConnId
}

func (l byID) Len() int {
	return len(l.Conns)
}
func (l byID) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// byIDDesc
type byIDDesc struct{ Conns []*connzResp }

func (l byIDDesc) Less(i, j int) bool {
	return l.Conns[i].ConnId > l.Conns[j].ConnId
}

func (l byIDDesc) Len() int {
	return len(l.Conns)
}
func (l byIDDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// inMsg
type byInMsg struct{ Conns []*connzResp }

func (l byInMsg) Less(i, j int) bool {
	return l.Conns[i].InMsgCount.Load() < l.Conns[j].InMsgCount.Load()
}
func (l byInMsg) Len() int      { return len(l.Conns) }
func (l byInMsg) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byInMsgDesc struct{ Conns []*connzResp }

func (l byInMsgDesc) Less(i, j int) bool {
	return l.Conns[i].InMsgCount.Load() > l.Conns[j].InMsgCount.Load()
}

func (l byInMsgDesc) Len() int      { return len(l.Conns) }
func (l byInMsgDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// byOutMsg

type byOutMsg struct{ Conns []*connzResp }

func (l byOutMsg) Less(i, j int) bool {
	return l.Conns[i].OutMsgCount.Load() < l.Conns[j].OutMsgCount.Load()
}
func (l byOutMsg) Len() int      { return len(l.Conns) }
func (l byOutMsg) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byOutMsgDesc struct{ Conns []*connzResp }

func (l byOutMsgDesc) Less(i, j int) bool {
	return l.Conns[i].OutMsgCount.Load() > l.Conns[j].OutMsgCount.Load()
}
func (l byOutMsgDesc) Len() int      { return len(l.Conns) }
func (l byOutMsgDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// byInMsgBytes

type byInMsgBytes struct{ Conns []*connzResp }

func (l byInMsgBytes) Less(i, j int) bool {
	return l.Conns[i].InMsgByteCount.Load() < l.Conns[j].InMsgByteCount.Load()
}
func (l byInMsgBytes) Len() int      { return len(l.Conns) }
func (l byInMsgBytes) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byInMsgBytesDesc struct{ Conns []*connzResp }

func (l byInMsgBytesDesc) Less(i, j int) bool {
	return l.Conns[i].InMsgByteCount.Load() > l.Conns[j].InMsgByteCount.Load()
}
func (l byInMsgBytesDesc) Len() int      { return len(l.Conns) }
func (l byInMsgBytesDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// outMsgBytes

type byOutMsgBytes struct{ Conns []*connzResp }

func (l byOutMsgBytes) Less(i, j int) bool {
	return l.Conns[i].OutMsgByteCount.Load() < l.Conns[j].OutMsgByteCount.Load()
}
func (l byOutMsgBytes) Len() int      { return len(l.Conns) }
func (l byOutMsgBytes) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byOutMsgBytesDesc struct{ Conns []*connzResp }

func (l byOutMsgBytesDesc) Less(i, j int) bool {
	return l.Conns[i].OutMsgByteCount.Load() > l.Conns[j].OutMsgByteCount.Load()
}
func (l byOutMsgBytesDesc) Len() int      { return len(l.Conns) }
func (l byOutMsgBytesDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// outPacketBytes

type byOutPacketBytes struct{ Conns []*connzResp }

func (l byOutPacketBytes) Less(i, j int) bool {
	return l.Conns[i].OutPacketByteCount.Load() < l.Conns[j].OutPacketByteCount.Load()
}

func (l byOutPacketBytes) Len() int      { return len(l.Conns) }
func (l byOutPacketBytes) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byOutPacketBytesDesc struct{ Conns []*connzResp }

func (l byOutPacketBytesDesc) Less(i, j int) bool {
	return l.Conns[i].OutPacketByteCount.Load() > l.Conns[j].OutPacketByteCount.Load()
}

func (l byOutPacketBytesDesc) Len() int { return len(l.Conns) }

func (l byOutPacketBytesDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// inPacketBytes

type byInPacketBytes struct{ Conns []*connzResp }

func (l byInPacketBytes) Less(i, j int) bool {
	return l.Conns[i].InPacketByteCount.Load() < l.Conns[j].InPacketByteCount.Load()
}

func (l byInPacketBytes) Len() int      { return len(l.Conns) }
func (l byInPacketBytes) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byInPacketBytesDesc struct{ Conns []*connzResp }

func (l byInPacketBytesDesc) Less(i, j int) bool {
	return l.Conns[i].InPacketByteCount.Load() > l.Conns[j].InPacketByteCount.Load()
}

func (l byInPacketBytesDesc) Len() int { return len(l.Conns) }

func (l byInPacketBytesDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// outPacket

type byOutPacket struct{ Conns []*connzResp }

func (l byOutPacket) Less(i, j int) bool {
	return l.Conns[i].OutPacketCount.Load() < l.Conns[j].OutPacketCount.Load()
}

func (l byOutPacket) Len() int { return len(l.Conns) }

func (l byOutPacket) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byOutPacketDesc struct{ Conns []*connzResp }

func (l byOutPacketDesc) Less(i, j int) bool {
	return l.Conns[i].OutPacketCount.Load() > l.Conns[j].OutPacketCount.Load()
}

func (l byOutPacketDesc) Len() int { return len(l.Conns) }

func (l byOutPacketDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// inPacket

type byInPacket struct{ Conns []*connzResp }

func (l byInPacket) Less(i, j int) bool {
	return l.Conns[i].InPacketCount.Load() < l.Conns[j].InPacketCount.Load()
}

func (l byInPacket) Len() int { return len(l.Conns) }

func (l byInPacket) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byInPacketDesc struct{ Conns []*connzResp }

func (l byInPacketDesc) Less(i, j int) bool {
	return l.Conns[i].InPacketCount.Load() > l.Conns[j].InPacketCount.Load()
}

func (l byInPacketDesc) Len() int { return len(l.Conns) }

func (l byInPacketDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// PendingBytes

// type byPendingBytes struct{ Conns []*reactor.Conn }

// func (l byPendingBytes) Less(i, j int) bool {
// 	return l.Conns[i].OutboundBuffer().BoundBufferSize() < l.Conns[j].OutboundBuffer().BoundBufferSize()
// }
// func (l byPendingBytes) Len() int      { return len(l.Conns) }
// func (l byPendingBytes) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// type byPendingBytesDesc struct{ Conns []*reactor.Conn }

// func (l byPendingBytesDesc) Less(i, j int) bool {
// 	return l.Conns[i].OutboundBuffer().BoundBufferSize() > l.Conns[j].OutboundBuffer().BoundBufferSize()
// }
// func (l byPendingBytesDesc) Len() int      { return len(l.Conns) }
// func (l byPendingBytesDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// uptime

type byUptime struct{ Conns []*connzResp }

func (l byUptime) Less(i, j int) bool {
	return l.Conns[i].Uptime < l.Conns[j].Uptime
}
func (l byUptime) Len() int      { return len(l.Conns) }
func (l byUptime) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

type byUptimeDesc struct{ Conns []*connzResp }

func (l byUptimeDesc) Less(i, j int) bool {
	return l.Conns[i].Uptime > l.Conns[j].Uptime
}
func (l byUptimeDesc) Len() int      { return len(l.Conns) }
func (l byUptimeDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// idle
type byIdle struct{ Conns []*connzResp }

func (l byIdle) Less(i, j int) bool {
	return l.Conns[i].LastActive < l.Conns[j].LastActive
}
func (l byIdle) Len() int      { return len(l.Conns) }
func (l byIdle) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// idleDesc
type byIdleDesc struct{ Conns []*connzResp }

func (l byIdleDesc) Less(i, j int) bool {
	return l.Conns[i].LastActive > l.Conns[j].LastActive
}
func (l byIdleDesc) Len() int      { return len(l.Conns) }
func (l byIdleDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// protoVersion
type byProtoVersion struct{ Conns []*connzResp }

func (l byProtoVersion) Less(i, j int) bool {
	return l.Conns[i].ProtoVersion < l.Conns[j].ProtoVersion
}
func (l byProtoVersion) Len() int      { return len(l.Conns) }
func (l byProtoVersion) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }

// protoVersionDesc
type byProtoVersionDesc struct{ Conns []*connzResp }

func (l byProtoVersionDesc) Less(i, j int) bool {
	return l.Conns[i].ProtoVersion > l.Conns[j].ProtoVersion
}

func (l byProtoVersionDesc) Len() int      { return len(l.Conns) }
func (l byProtoVersionDesc) Swap(i, j int) { l.Conns[i], l.Conns[j] = l.Conns[j], l.Conns[i] }
