package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/WuKongIM/wkrpc"
	"go.uber.org/zap"
)

func (a *rpc) streamOpen(c *wkrpc.Context) {
	streamInfo := &pluginproto.Stream{}
	err := streamInfo.Unmarshal(c.Body())
	if err != nil {
		a.Error("Stream unmarshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	streamNo, err := a.requestStreamOpen(streamInfo)
	if err != nil {
		a.Error("requestStreamOpen failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	result := pluginproto.StreamOpenResp{
		StreamNo: streamNo,
	}
	data, err := result.Marshal()
	if err != nil {
		a.Error("StreamOpenResult marshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)
}

func (a *rpc) streamClose(c *wkrpc.Context) {
	req := &pluginproto.StreamCloseReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		a.Error("StreamCloseReq unmarshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	err = a.requestStreamClose(req)
	if err != nil {
		a.Error("requestStreamClose failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	c.WriteOk()
}

func (a *rpc) streamWrite(c *wkrpc.Context) {
	req := &pluginproto.StreamWriteReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		a.Error("StreamWriteReq unmarshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	fmt.Println("req---->", req)

	// 转换假频道为真频道
	if req.ChannelType == uint32(wkproto.ChannelTypePerson) && options.G.IsFakeChannel(req.ChannelId) {
		from, to := options.GetFromUIDAndToUIDWith(req.ChannelId)
		fromUid := req.FromUid
		if strings.TrimSpace(fromUid) == "" {
			fromUid = options.G.SystemUID
		}
		var realChannelId string
		if fromUid == from {
			realChannelId = to
		} else {
			realChannelId = from
		}
		req.ChannelId = realChannelId
	}

	resp, err := a.requestStreamWrite(req)
	if err != nil {
		a.Error("requestStreamWrite failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	data, err := resp.Marshal()
	if err != nil {
		a.Error("StreamWriteResp marshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	c.Write(data)
}

func (a *rpc) requestStreamOpen(stream *pluginproto.Stream) (string, error) {

	node := service.Cluster.NodeInfoById(options.G.Cluster.NodeId)
	if node == nil {
		a.Error("requestStreamOpen: node not found", zap.Uint64("nodeId", options.G.Cluster.NodeId))
		return "", errors.New("requestStreamOpen: node not found")
	}

	reqUrl := node.ApiServerAddr + "/stream/open"

	noPersist := uint8(0)
	redot := uint8(0)
	syncOnce := uint8(0)
	if stream.Header != nil {
		noPersist = wkutil.BoolToUint8(stream.Header.NoPersist)
		redot = wkutil.BoolToUint8(stream.Header.RedDot)
		syncOnce = wkutil.BoolToUint8(stream.Header.SyncOnce)
	}

	resp, err := a.post(reqUrl, []byte(wkutil.ToJSON(map[string]interface{}{
		"header": map[string]interface{}{
			"no_persist": noPersist,
			"red_dot":    redot,
			"sync_once":  syncOnce,
		},
		"client_msg_no": stream.ClientMsgNo,
		"from_uid":      stream.FromUid,
		"channel_id":    stream.ChannelId,
		"channel_type":  stream.ChannelType,
		"payload":       stream.Payload,
	})))
	if err != nil {
		a.Error("requestStreamOpen: post failed", zap.Error(err))
		return "", err
	}

	resultMap, err := wkutil.JSONToMap(resp.Body)
	if err != nil {
		a.Error("requestStreamOpen: JSONToMap failed", zap.Error(err))
		return "", err
	}

	if resultMap["stream_no"] == nil {
		return "", errors.New("requestStreamOpen: stream_no not found")
	}
	return resultMap["stream_no"].(string), nil
}

func (a *rpc) requestStreamClose(req *pluginproto.StreamCloseReq) error {

	node := service.Cluster.NodeInfoById(options.G.Cluster.NodeId)
	if node == nil {
		a.Error("requestStreamClose: node not found", zap.Uint64("nodeId", options.G.Cluster.NodeId))
		return errors.New("requestStreamClose: node not found")
	}

	reqUrl := node.ApiServerAddr + "/stream/close"

	_, err := a.post(reqUrl, []byte(wkutil.ToJSON(map[string]interface{}{
		"stream_no":    req.StreamNo,
		"channel_id":   req.ChannelId,
		"channel_type": req.ChannelType,
	})))
	if err != nil {
		return err
	}
	return nil
}

func (a *rpc) requestStreamWrite(req *pluginproto.StreamWriteReq) (*pluginproto.StreamWriteResp, error) {

	node := service.Cluster.NodeInfoById(options.G.Cluster.NodeId)
	if node == nil {
		a.Error("requestStreamWrite: node not found", zap.Uint64("nodeId", options.G.Cluster.NodeId))
		return nil, errors.New("requestStreamWrite: node not found")
	}

	reqUrl := node.ApiServerAddr + "/message/send"

	noPersist := uint8(0)
	redot := uint8(0)
	syncOnce := uint8(0)
	if req.Header != nil {
		noPersist = wkutil.BoolToUint8(req.Header.NoPersist)
		redot = wkutil.BoolToUint8(req.Header.RedDot)
		syncOnce = wkutil.BoolToUint8(req.Header.SyncOnce)
	}

	resp, err := a.post(reqUrl, []byte(wkutil.ToJSON(map[string]interface{}{
		"header": map[string]interface{}{
			"no_persist": noPersist,
			"red_dot":    redot,
			"sync_once":  syncOnce,
		},
		"stream_no":     req.StreamNo,
		"channel_id":    req.ChannelId,
		"channel_type":  req.ChannelType,
		"client_msg_no": req.ClientMsgNo,
		"from_uid":      req.FromUid,
		"payload":       req.Payload,
	})))
	if err != nil {
		return nil, err
	}

	resultMap, err := wkutil.JSONToMap(resp.Body)
	if err != nil {
		a.Error("requestStreamWrite: JSONToMap failed", zap.Error(err))
		return nil, err
	}

	streamWriteResp := &pluginproto.StreamWriteResp{}
	if resultMap["client_msg_no"] != nil {
		streamWriteResp.ClientMsgNo = resultMap["client_msg_no"].(string)
	}

	if resultMap["message_id"] != nil {
		streamWriteResp.MessageId, _ = resultMap["message_id"].(json.Number).Int64()
	}

	return streamWriteResp, nil
}
