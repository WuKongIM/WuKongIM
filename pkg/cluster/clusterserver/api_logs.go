package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

func (s *Server) messageTrace(c *wkhttp.Context) {
	clientMsgNo := c.Query("client_msg_no")
	width := wkutil.ParseFloat64(c.Query("width"))
	since := wkutil.ParseInt64(c.Query("since")) // 查询多久的轨迹，单位秒 比如 60 为查询60秒内的轨迹
	// height := wkutil.ParseFloat64(c.Query("height"))

	if clientMsgNo == "" {
		c.ResponseError(errors.New("client_msg_no can't be empty"))
		return
	}

	if since <= 0 {
		since = 60 * 60 // 默认查询1小时内的轨迹
	}

	start := time.Now().Add(-(time.Second * time.Duration(since)))
	end := time.Now()

	// ==================  请求轨迹日志 ==================

	queryStr := `{trace = "1"} | json`
	if clientMsgNo != "" {
		queryStr = fmt.Sprintf(`%s | no="%s"`, queryStr, clientMsgNo)
	}
	streamGroups, err := s.requestTraceStreams(queryStr, start, end)
	if err != nil {
		s.Error("requestTraceStreams error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if len(streamGroups) == 0 {
		s.Error("requestTraceStreams empty")
		c.ResponseError(errors.New("没有找到轨迹日志"))
		return
	}

	// 创建一个消息轨迹模版
	traceTemplate := newMessageTraceTemple(width)

	trace := &Trace{
		Nodes: traceTemplate.Nodes,
		Edges: traceTemplate.Edges,
	}

	// 从trace里获取节点
	getSpanNodeFromTrace := func(id string) *SpanNode {
		for _, node := range trace.Nodes {
			if node.Id == id {
				return node
			}
		}
		return nil
	}

	// ==================  匹配模版里的节点并填充数据 ==================

	var messageId int64 // 消息id
	for _, streamGroup := range streamGroups {

		node := getSpanNodeFromTrace(streamGroup.Action)
		if node == nil {
			continue
		}

		stream := streamGroup.First()

		node.Shape = "spanNode"
		node.NodeId = stream.getNodeId()
		node.Time = wkutil.ToyyyyMMddHHmmss(stream.getTraceTime())
		node.Icon = stream.getIcon()
		node.genDescription()

		action := stream.getAction()

		if action == "processMessage" {
			node.Data = stream

			deivceFlag := stream.getInt("deviceFlag")
			deviceLevel := stream.getInt("deviceLevel")
			channelType := stream.getInt("channelType")

			node.Data["channelType"] = getChannelTypeFormat(uint8(channelType))
			node.Data["deviceLevel"] = wkproto.DeviceLevel(deviceLevel).String()
			node.Data["deviceFlag"] = wkproto.DeviceFlag(deivceFlag).String()

			messageId = stream.getInt64("messageId")
		}
	}

	// ==================  动态生成投递流程节点 ==================

	// 获取投递节点
	processDeliverNode := getSpanNodeFromTrace("processDeliver")
	getStreamGroup := func(action string) *StreamGroup {
		for _, streamGroup := range streamGroups {
			if streamGroup.Action == action {
				return streamGroup
			}
		}
		return nil
	}

	// 获取投递节点的轨迹组
	deliverNodeStreamGroup := getStreamGroup("deliverNode")
	leftSpace := 40
	getX := func(i int, count int, initX float64, width float64) float64 {

		if count == 0 {
			return 0
		}

		totalWidth := width*float64(count) + float64(leftSpace*(count-1))

		leftX := (totalWidth/2 - width/2) + initX

		return leftX - float64(i)*(width+float64(leftSpace))

	}
	if deliverNodeStreamGroup != nil {
		nodeCount := len(deliverNodeStreamGroup.Streams)
		sort.Slice(deliverNodeStreamGroup.Streams, func(i, j int) bool {
			return deliverNodeStreamGroup.Streams[i].getNodeId() > deliverNodeStreamGroup.Streams[j].getNodeId()
		})
		for i, stream := range deliverNodeStreamGroup.Streams {
			nodeId := stream.getNodeId()
			node := new(SpanNode)
			node.Id = fmt.Sprintf("%s-%d", stream.getAction(), nodeId)
			node.Shape = "spanNode"
			node.NodeId = nodeId
			node.Name = fmt.Sprintf("%s%d", stream.getName(), nodeId)
			node.Time = wkutil.ToyyyyMMddHHmmss(stream.getTraceTime())
			node.Icon = stream.getIcon()
			node.Width = processDeliverNode.Width
			node.Height = processDeliverNode.Height
			node.X = getX(i, nodeCount, processDeliverNode.X, processDeliverNode.Width)
			node.Y = processDeliverNode.Y + processDeliverNode.Height + 60
			node.genDescription()
			node.Description += fmt.Sprintf(" 用户:%d", stream.getInt("userCount"))
			trace.Nodes = append(trace.Nodes, node)

			edge := new(SpanEdge)
			edge.Source = processDeliverNode.Id
			edge.Target = node.Id
			trace.Edges = append(trace.Edges, edge)
		}
	}

	deliverStreams := make([]Stream, 0)

	// 获取在线投递消息的轨迹组
	deliverOnlineStreamGroup := getStreamGroup("deliverOnline")
	if deliverOnlineStreamGroup != nil {
		deliverStreams = append(deliverStreams, deliverOnlineStreamGroup.Streams...)
	}

	// 获取离线投递消息的轨迹组
	deliverOfflineStreamGroup := getStreamGroup("deliverOffline")
	if deliverOfflineStreamGroup != nil {
		deliverStreams = append(deliverStreams, deliverOfflineStreamGroup.Streams...)
	}

	if len(deliverStreams) > 0 {
		sort.Slice(deliverStreams, func(i, j int) bool {
			return deliverStreams[i].getNodeId() > deliverStreams[j].getNodeId()
		})
		for i, stream := range deliverStreams {

			nodeId := stream.getNodeId()
			deliverNodeSpanNode := getSpanNodeFromTrace(fmt.Sprintf("deliverNode-%d", nodeId))
			if deliverNodeSpanNode == nil { // 理论上一定有deliverNode节点
				continue
			}

			node := new(SpanNode)
			node.Id = fmt.Sprintf("%s-%d", stream.getAction(), nodeId)
			node.Shape = "spanNode"
			node.NodeId = nodeId
			node.Name = stream.getName()
			node.Time = wkutil.ToyyyyMMddHHmmss(stream.getTraceTime())
			node.Icon = stream.getIcon()
			node.Width = deliverNodeSpanNode.Width
			node.Height = deliverNodeSpanNode.Height
			node.X = getX(i, len(deliverStreams), deliverNodeSpanNode.X, deliverNodeSpanNode.Width)
			node.Y = deliverNodeSpanNode.Y + deliverNodeSpanNode.Height + 60
			node.genDescription()
			node.Description += fmt.Sprintf(" 用户:%d", stream.getInt("userCount"))
			if stream.getAction() == "deliverOnline" {
				node.Description += fmt.Sprintf(" 连接:%d", stream.getInt("connCount"))
			}
			node.Data = make(map[string]interface{})
			node.Data["uids"] = stream.getString("uids")

			trace.Nodes = append(trace.Nodes, node)

			edge := new(SpanEdge)
			edge.Source = deliverNodeSpanNode.Id
			edge.Target = node.Id
			trace.Edges = append(trace.Edges, edge)
		}
	}

	if deliverOnlineStreamGroup != nil && len(deliverOnlineStreamGroup.Streams) > 0 {
		metris, err := s.requestTraceMetric(fmt.Sprintf(`sum by(messageId, nodeId, action) (count_over_time({trace="1"} | json | action = "processRecvack" | messageId = "%d" [1h]))`, messageId), start, end)
		if err != nil {
			s.Error("requestTraceMetric error", zap.Error(err))
			c.ResponseError(err)
			return
		}
		if len(metris) > 0 {
			sort.Slice(metris, func(i, j int) bool {
				return metris[i].getNodeId() > metris[j].getNodeId()
			})

			for _, metri := range metris {
				nodeId := metri.getNodeId()
				deliverOnlineNode := getSpanNodeFromTrace(fmt.Sprintf("deliverOnline-%d", nodeId))
				if deliverOnlineNode == nil {
					continue
				}

				node := new(SpanNode)
				node.Id = fmt.Sprintf("processRecvack-%d", nodeId)
				node.Shape = "spanNode"
				node.NodeId = nodeId
				node.Name = metri.getName()
				node.X = deliverOnlineNode.X
				node.Y = deliverOnlineNode.Y + deliverOnlineNode.Height + 60
				node.Width = deliverOnlineNode.Width
				node.Height = deliverOnlineNode.Height
				node.genDescription()
				node.Data = metri
				if node.Data == nil {
					node.Data = make(map[string]interface{})
				}

				values := metri.getObjects("values")
				if len(values) > 0 {
					firstValue := values[0]
					if vv, ok := firstValue.([]interface{}); ok {
						if len(vv) > 1 {
							v := vv[1]
							countStr := v.(string)
							node.Data["count"] = wkutil.ParseInt(countStr)
							node.Description += fmt.Sprintf(" 连接:%s", countStr)
						}
					}
				}

				trace.Nodes = append(trace.Nodes, node)

				edge := new(SpanEdge)
				edge.Source = node.Id
				edge.Target = deliverOnlineNode.Id
				trace.Edges = append(trace.Edges, edge)
			}
		}

	}

	c.JSON(http.StatusOK, trace)

}

func (s *Server) messageRecvackTrace(c *wkhttp.Context) {
	nodeId := wkutil.ParseUint64(c.Query("node_id"))
	messageId := wkutil.ParseInt64(c.Query("message_id"))
	since := wkutil.ParseInt64(c.Query("since")) // 查询多久的轨迹，单位秒 比如 60 为查询60秒内的轨迹

	if since <= 0 {
		since = 60 * 60 // 默认查询1小时内的轨迹
	}

	start := time.Now().Add(-(time.Second * time.Duration(since)))
	end := time.Now()

	streamGroups, err := s.requestTraceStreams(fmt.Sprintf(`{trace="1"} | json | action = "processRecvack" | messageId = "%d"`, messageId), start, end)
	if err != nil {
		s.Error("requestTraceStreams error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	resultMaps := make([]map[string]interface{}, 0)
	for _, streamGroup := range streamGroups {
		if streamGroup.Action != "processRecvack" {
			continue
		}
		sort.Slice(streamGroup.Streams, func(i, j int) bool {
			return streamGroup.Streams[i].getTraceTime().Before(streamGroup.Streams[j].getTraceTime())
		})
		for _, stream := range streamGroup.Streams {
			if stream.getNodeId() == nodeId {
				resultMaps = append(resultMaps, map[string]interface{}{
					"node_id":     stream.getNodeId(),
					"uid":         stream.getString("uid"),
					"device_id":   stream.getString("deviceId"),
					"message_id":  stream.getInt64("messageId"),
					"message_seq": stream.getInt64("messageSeq"),
					"time":        wkutil.ToyyyyMMddHHmmss(stream.getTraceTime()),
					"conn_id":     stream.getInt64("connId"),
				})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"total": len(resultMaps),
		"data":  resultMaps,
	})

}

var upGrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (s *Server) requestTraceStreams(query string, start, end time.Time) ([]*StreamGroup, error) {

	queryParams := map[string]string{
		"query": query,
		"start": fmt.Sprintf("%d", start.UnixNano()),
		"end":   fmt.Sprintf("%d", end.UnixNano()),
	}

	apiUrl := fmt.Sprintf("%s/loki/api/v1/query_range", strings.TrimSuffix(s.opts.LokiUrl, "/"))

	resp, err := network.Get(apiUrl, queryParams, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		s.Error("requestTraces failed", zap.Int("status", resp.StatusCode), zap.String("body", resp.Body))
		return nil, fmt.Errorf("requestTraces failed, status code: %d", resp.StatusCode)
	}
	var returnMap map[string]interface{}
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &returnMap)
	if err != nil {
		return nil, err
	}
	if returnMap["data"] != nil {
		dataMap := returnMap["data"].(map[string]interface{})
		if dataMap["result"] != nil {
			results := dataMap["result"].([]interface{})
			streams := make([]Stream, 0, len(results))
			for _, result := range results {
				resultMap := result.(map[string]interface{})
				if resultMap["stream"] == nil {
					continue
				}
				steamMap := resultMap["stream"].(map[string]interface{})
				streams = append(streams, steamMap)
			}
			sort.Slice(streams, func(i, j int) bool {

				return streams[i].getTraceTime().Before(streams[j].getTraceTime())
			})

			// 获取stream所在的group
			getStreamGroup := func(action string, groups []*StreamGroup) *StreamGroup {
				if action == "" || len(groups) == 0 {
					return nil
				}
				for _, group := range groups {
					for _, groupStream := range group.Streams {
						if groupStream.getString("action") == action {
							return group
						}
					}
				}
				return nil
			}

			// 对stream进行group
			groups := make([]*StreamGroup, 0, len(streams))
			for _, stream := range streams {

				parentAction := stream.getString("actionP")

				// 如果有父级action，则将stream放到父级的SubGroups里
				if strings.TrimSpace(parentAction) != "" {
					parentGroup := getStreamGroup(parentAction, groups)
					if parentGroup == nil {
						continue
					}
					if len(parentGroup.SubGroups) > 0 {
						subGroup := getStreamGroup(stream.getString("action"), parentGroup.SubGroups)
						if subGroup == nil {
							parentGroup.SubGroups = append(parentGroup.SubGroups, &StreamGroup{Action: stream.getString("action"), Streams: []Stream{stream}})
						} else {
							subGroup.Streams = append(subGroup.Streams, stream)
						}
					}
					continue
				}

				group := getStreamGroup(stream.getString("action"), groups)
				if group == nil {
					groups = append(groups, &StreamGroup{Action: stream.getString("action"), Streams: []Stream{stream}})
					continue
				}
				group.Streams = append(group.Streams, stream)
			}

			return groups, nil
		}
	}
	return nil, err

}

func (s *Server) requestTraceMetric(query string, start, end time.Time) ([]Stream, error) {
	queryParams := map[string]string{
		"query": query,
		"start": fmt.Sprintf("%d", start.UnixNano()),
		"end":   fmt.Sprintf("%d", end.UnixNano()),
	}
	apiUrl := fmt.Sprintf("%s/loki/api/v1/query_range", strings.TrimSuffix(s.opts.LokiUrl, "/"))

	resp, err := network.Get(apiUrl, queryParams, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		s.Error("requestTraceMetric failed", zap.Int("status", resp.StatusCode), zap.String("body", resp.Body))
		return nil, fmt.Errorf("requestTraceMetric failed, status code: %d", resp.StatusCode)
	}

	var returnMap map[string]interface{}
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &returnMap)
	if err != nil {
		return nil, err
	}
	streams := make([]Stream, 0)
	if returnMap["data"] != nil {
		dataMap := returnMap["data"].(map[string]interface{})
		if dataMap["result"] != nil {
			results := dataMap["result"].([]interface{})
			for _, result := range results {
				resultMap := result.(map[string]interface{})
				if resultMap["metric"] == nil {
					continue
				}
				metricMap := resultMap["metric"].(map[string]interface{})
				metricMap["values"] = resultMap["values"]
				streams = append(streams, metricMap)
			}
		}
	}
	return streams, nil
}

// tail日志
func (s *Server) logsTail(c *wkhttp.Context) {
	// 升级get请求为webSocket协议
	ws, err := upGrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	go s.loopLogsTailWs(ws)
}

func (s *Server) loopLogsTailWs(ws *websocket.Conn) {

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)

	u, err := url.Parse(s.opts.LokiUrl)
	if err != nil {
		s.Error("parse loki url error", zap.Error(err))
		cancel()
		return
	}
	u.Scheme = "ws"
	u.Path = "/loki/api/v1/tail"

	fmt.Println("loki url:", u.String())

	lokiConn, _, err := websocket.DefaultDialer.DialContext(timeoutCtx, u.String(), nil)
	if err != nil {
		s.Error("dial loki error", zap.Error(err))
		cancel()
		return
	}
	cancel()

	defer func() {
		lokiConn.Close()
		ws.Close()
	}()

	for {
		messageType, data, err := lokiConn.ReadMessage()
		if err != nil {
			break
		}
		err = ws.WriteMessage(messageType, data)
		if err != nil {
			break
		}
	}

}
