package ingress

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

type Client struct {
	wklog.Log
}

func NewClient() *Client {
	return &Client{
		Log: wklog.NewWKLog("ingress.Client"),
	}
}

// 请求获取指定节点的用户
func (c *Client) RequestTag(toNodeId uint64, req *TagReq) (*TagResp, error) {
	data, err := req.encode()
	if err != nil {
		return nil, err
	}
	resp, err := c.request(toNodeId, "/wk/ingress/getTag", data)
	if err != nil {
		return nil, err
	}
	err = c.handleRespError(resp)
	if err != nil {
		return nil, err
	}
	tagResp := &TagResp{}
	err = tagResp.decode(resp.Body)
	if err != nil {
		return nil, err
	}
	return tagResp, nil
}

// UpdateTag 更新tag
func (c *Client) UpdateTag(nodeId uint64, req *TagUpdateReq) error {
	data, err := req.Encode()
	if err != nil {
		return err
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	resp, err := service.Cluster.RequestWithContext(timeoutCtx, nodeId, "/wk/ingress/updateTag", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.StatusOK {
		return errors.New("updateOrMakeTag: status error")
	}
	return nil
}

func (c *Client) AddTag(nodeId uint64, req *TagAddReq) error {
	data, err := req.Encode()
	if err != nil {
		return err
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	resp, err := service.Cluster.RequestWithContext(timeoutCtx, nodeId, "/wk/ingress/addTag", data)
	if err != nil {
		return err
	}

	if resp.Status != proto.StatusOK {
		return errors.New("addTag: status error")
	}
	return nil
}

// 个人聊天判断接受者是否允许发送消息
func (c *Client) RequestAllowSendForPerson(toNodeId uint64, from, to string) (*proto.Response, error) {

	req := &AllowSendReq{
		From: from,
		To:   to,
	}
	data, err := req.encode()
	if err != nil {
		return nil, err
	}
	return c.request(toNodeId, "/wk/ingress/allowSend", data)
}

func (c *Client) RequestSubscribers(toNodeId uint64, channelId string, channelType uint8) ([]string, error) {

	req := &ChannelReq{
		ChannelId:   channelId,
		ChannelType: channelType,
	}

	data, err := req.Encode()
	if err != nil {
		return nil, err
	}
	resp, err := c.request(toNodeId, "/wk/ingress/getSubscribers", data)
	if err != nil {
		return nil, err
	}
	err = c.handleRespError(resp)
	if err != nil {
		return nil, err
	}
	subResp := &SubscribersResp{}
	err = subResp.Decode(resp.Body)
	if err != nil {
		return nil, err
	}
	return subResp.Subscribers, nil
}

func (c *Client) request(toNodeId uint64, path string, body []byte) (*proto.Response, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()
	resp, err := service.Cluster.RequestWithContext(timeoutCtx, toNodeId, path, body)
	if err != nil {
		c.Error("request failed", zap.Error(err), zap.String("path", path), zap.Int("body", len(body)))
		return nil, err
	}
	return resp, nil
}

func (c *Client) handleRespError(resp *proto.Response) error {
	if resp.Status != proto.StatusOK {
		return fmt.Errorf("resp status error[%d]", resp.Status)
	}
	return nil
}
