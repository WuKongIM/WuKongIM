package node

import (
	"fmt"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
)

var (
	managerPluginRequestMagic  = [...]byte{'W', 'K', 'V', 'J', 1}
	managerPluginResponseMagic = [...]byte{'W', 'K', 'V', 'j', 1}
)

const (
	managerPluginOpList        = "list_plugins"
	managerPluginOpGet         = "get_plugin"
	managerPluginOpHTTPForward = "http_forward"

	managerPluginOpListID byte = iota + 1
	managerPluginOpGetID
	managerPluginOpHTTPForwardID

	maxManagerPluginRPCCollectionLen = 4096
)

type managerPluginRPCRequest struct {
	Op         string
	NodeID     uint64
	PluginNo   string
	ForwardReq *pluginproto.ForwardHttpReq
}

type managerPluginRPCResponse struct {
	Status      string
	Plugins     []managementusecase.Plugin
	Plugin      managementusecase.Plugin
	ForwardResp *pluginproto.HttpResponse
}

func encodeManagerPluginRequest(req managerPluginRPCRequest) ([]byte, error) {
	opID, err := managerPluginOpID(req.Op)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, 64)
	dst = append(dst, managerPluginRequestMagic[:]...)
	dst = append(dst, opID)
	dst = appendUvarint(dst, req.NodeID)
	dst = appendString(dst, req.PluginNo)
	if req.ForwardReq != nil {
		dst = appendForwardHTTPReq(dst, req.ForwardReq)
	}
	return dst, nil
}

func decodeManagerPluginRequest(body []byte) (managerPluginRPCRequest, error) {
	if !hasMagic(body, managerPluginRequestMagic[:]) {
		return managerPluginRPCRequest{}, fmt.Errorf("internalv2/access/node: invalid manager plugin request codec")
	}
	offset := len(managerPluginRequestMagic)
	opID, next, err := readByte(body, offset, "manager plugin op")
	if err != nil {
		return managerPluginRPCRequest{}, err
	}
	offset = next
	op, err := managerPluginOpFromID(opID)
	if err != nil {
		return managerPluginRPCRequest{}, err
	}
	nodeID, offset, err := readUvarint(body, offset)
	if err != nil {
		return managerPluginRPCRequest{}, err
	}
	pluginNo, offset, err := readString(body, offset)
	if err != nil {
		return managerPluginRPCRequest{}, err
	}
	var forwardReq *pluginproto.ForwardHttpReq
	if offset < len(body) {
		forwardReq, offset, err = readForwardHTTPReq(body, offset)
		if err != nil {
			return managerPluginRPCRequest{}, err
		}
		if pluginNo == "" {
			pluginNo = forwardReq.GetPluginNo()
		}
	}
	if offset != len(body) {
		return managerPluginRPCRequest{}, fmt.Errorf("internalv2/access/node: trailing manager plugin request bytes")
	}
	return managerPluginRPCRequest{Op: op, NodeID: nodeID, PluginNo: pluginNo, ForwardReq: forwardReq}, nil
}

func encodeManagerPluginResponse(resp managerPluginRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, 256)
	dst = append(dst, managerPluginResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendManagerPlugins(dst, resp.Plugins)
	dst = appendManagerPlugin(dst, resp.Plugin)
	if resp.ForwardResp != nil {
		dst = appendHTTPResponse(dst, resp.ForwardResp)
	}
	return dst, nil
}

func decodeManagerPluginResponse(body []byte) (managerPluginRPCResponse, error) {
	if !hasMagic(body, managerPluginResponseMagic[:]) {
		return managerPluginRPCResponse{}, fmt.Errorf("internalv2/access/node: invalid manager plugin response codec")
	}
	offset := len(managerPluginResponseMagic)
	var resp managerPluginRPCResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return managerPluginRPCResponse{}, err
	}
	if resp.Plugins, offset, err = readManagerPlugins(body, offset); err != nil {
		return managerPluginRPCResponse{}, err
	}
	if resp.Plugin, offset, err = readManagerPlugin(body, offset); err != nil {
		return managerPluginRPCResponse{}, err
	}
	if offset < len(body) {
		resp.ForwardResp, offset, err = readHTTPResponse(body, offset)
		if err != nil {
			return managerPluginRPCResponse{}, err
		}
	}
	if offset != len(body) {
		return managerPluginRPCResponse{}, fmt.Errorf("internalv2/access/node: trailing manager plugin response bytes")
	}
	return resp, nil
}

func appendForwardHTTPReq(dst []byte, req *pluginproto.ForwardHttpReq) []byte {
	if req == nil {
		req = &pluginproto.ForwardHttpReq{}
	}
	dst = appendString(dst, req.GetPluginNo())
	dst = appendVarint(dst, req.GetToNodeId())
	dst = appendHTTPRequest(dst, req.GetRequest())
	return dst
}

func readForwardHTTPReq(body []byte, offset int) (*pluginproto.ForwardHttpReq, int, error) {
	var req pluginproto.ForwardHttpReq
	var err error
	if req.PluginNo, offset, err = readString(body, offset); err != nil {
		return nil, offset, err
	}
	if req.ToNodeId, offset, err = readVarint(body, offset); err != nil {
		return nil, offset, err
	}
	if req.Request, offset, err = readHTTPRequest(body, offset); err != nil {
		return nil, offset, err
	}
	return &req, offset, nil
}

func appendHTTPRequest(dst []byte, req *pluginproto.HttpRequest) []byte {
	if req == nil {
		req = &pluginproto.HttpRequest{}
	}
	dst = appendString(dst, req.GetMethod())
	dst = appendString(dst, req.GetPath())
	dst = appendStringMap(dst, req.GetHeaders())
	dst = appendStringMap(dst, req.GetQuery())
	dst = appendBytes(dst, req.GetBody())
	return dst
}

func readHTTPRequest(body []byte, offset int) (*pluginproto.HttpRequest, int, error) {
	var req pluginproto.HttpRequest
	var err error
	if req.Method, offset, err = readString(body, offset); err != nil {
		return nil, offset, err
	}
	if req.Path, offset, err = readString(body, offset); err != nil {
		return nil, offset, err
	}
	if req.Headers, offset, err = readStringMap(body, offset, "plugin http headers"); err != nil {
		return nil, offset, err
	}
	if req.Query, offset, err = readStringMap(body, offset, "plugin http query"); err != nil {
		return nil, offset, err
	}
	if req.Body, offset, err = readBytes(body, offset); err != nil {
		return nil, offset, err
	}
	return &req, offset, nil
}

func appendHTTPResponse(dst []byte, resp *pluginproto.HttpResponse) []byte {
	if resp == nil {
		resp = &pluginproto.HttpResponse{}
	}
	dst = appendVarint(dst, int64(resp.GetStatus()))
	dst = appendStringMap(dst, resp.GetHeaders())
	dst = appendBytes(dst, resp.GetBody())
	return dst
}

func readHTTPResponse(body []byte, offset int) (*pluginproto.HttpResponse, int, error) {
	var resp pluginproto.HttpResponse
	var signed int64
	var err error
	if signed, offset, err = readVarint(body, offset); err != nil {
		return nil, offset, err
	}
	resp.Status = int32(signed)
	if resp.Headers, offset, err = readStringMap(body, offset, "plugin http response headers"); err != nil {
		return nil, offset, err
	}
	if resp.Body, offset, err = readBytes(body, offset); err != nil {
		return nil, offset, err
	}
	return &resp, offset, nil
}

func appendStringMap(dst []byte, values map[string]string) []byte {
	dst = appendUvarint(dst, uint64(len(values)))
	if len(values) == 0 {
		return dst
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		dst = appendString(dst, key)
		dst = appendString(dst, values[key])
	}
	return dst
}

func readStringMap(body []byte, offset int, label string) (map[string]string, int, error) {
	n, offset, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if n > maxManagerPluginRPCCollectionLen {
		return nil, offset, fmt.Errorf("internalv2/access/node: too many %s: %d", label, n)
	}
	if n == 0 {
		return nil, offset, nil
	}
	values := make(map[string]string, n)
	for i := uint64(0); i < n; i++ {
		key, next, err := readString(body, offset)
		if err != nil {
			return nil, offset, err
		}
		offset = next
		value, next, err := readString(body, offset)
		if err != nil {
			return nil, offset, err
		}
		offset = next
		values[key] = value
	}
	return values, offset, nil
}

func appendManagerPlugins(dst []byte, items []managementusecase.Plugin) []byte {
	dst = appendUvarint(dst, uint64(len(items)))
	for _, item := range items {
		dst = appendManagerPlugin(dst, item)
	}
	return dst
}

func readManagerPlugins(body []byte, offset int) ([]managementusecase.Plugin, int, error) {
	n, offset, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if n > maxManagerPluginRPCCollectionLen {
		return nil, offset, fmt.Errorf("internalv2/access/node: too many manager plugins: %d", n)
	}
	items := make([]managementusecase.Plugin, 0, n)
	for i := uint64(0); i < n; i++ {
		item, next, err := readManagerPlugin(body, offset)
		if err != nil {
			return nil, offset, err
		}
		offset = next
		items = append(items, item)
	}
	return items, offset, nil
}

func appendManagerPlugin(dst []byte, item managementusecase.Plugin) []byte {
	dst = appendUvarint(dst, item.NodeID)
	dst = appendString(dst, item.No)
	dst = appendString(dst, item.Name)
	dst = appendString(dst, item.Version)
	dst = appendUvarint(dst, uint64(len(item.Methods)))
	for _, method := range item.Methods {
		dst = appendString(dst, string(method))
	}
	dst = appendVarint(dst, int64(item.Priority))
	dst = appendBoolByte(dst, item.PersistAfterSync)
	dst = appendBoolByte(dst, item.ReplySync)
	dst = appendString(dst, item.Status)
	dst = appendBoolByte(dst, item.Enabled)
	dst = appendUvarint(dst, uint64(item.IsAI))
	dst = appendVarint(dst, int64(item.PID))
	dst = appendVarint(dst, item.LastSeenAt.UnixNano())
	dst = appendString(dst, item.LastError)
	return dst
}

func readManagerPlugin(body []byte, offset int) (managementusecase.Plugin, int, error) {
	var item managementusecase.Plugin
	var value uint64
	var signed int64
	var err error
	if value, offset, err = readUvarint(body, offset); err != nil {
		return item, offset, err
	}
	item.NodeID = value
	if item.No, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.Name, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.Version, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	methodCount, offset, err := readUvarint(body, offset)
	if err != nil {
		return item, offset, err
	}
	if methodCount > maxManagerPluginRPCCollectionLen {
		return item, offset, fmt.Errorf("internalv2/access/node: too many manager plugin methods: %d", methodCount)
	}
	item.Methods = make([]pluginusecase.Method, 0, methodCount)
	for i := uint64(0); i < methodCount; i++ {
		method, next, err := readString(body, offset)
		if err != nil {
			return item, offset, err
		}
		offset = next
		item.Methods = append(item.Methods, pluginusecase.Method(method))
	}
	if signed, offset, err = readVarint(body, offset); err != nil {
		return item, offset, err
	}
	item.Priority = int(signed)
	if item.PersistAfterSync, offset, err = readBoolByte(body, offset, "manager plugin persist_after_sync"); err != nil {
		return item, offset, err
	}
	if item.ReplySync, offset, err = readBoolByte(body, offset, "manager plugin reply_sync"); err != nil {
		return item, offset, err
	}
	if item.Status, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.Enabled, offset, err = readBoolByte(body, offset, "manager plugin enabled"); err != nil {
		return item, offset, err
	}
	if value, offset, err = readUvarint(body, offset); err != nil {
		return item, offset, err
	}
	item.IsAI = uint8(value)
	if signed, offset, err = readVarint(body, offset); err != nil {
		return item, offset, err
	}
	item.PID = int(signed)
	if signed, offset, err = readVarint(body, offset); err != nil {
		return item, offset, err
	}
	if signed > 0 {
		item.LastSeenAt = time.Unix(0, signed).UTC()
	}
	if item.LastError, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	return item, offset, nil
}

func managerPluginOpID(op string) (byte, error) {
	switch op {
	case managerPluginOpList:
		return managerPluginOpListID, nil
	case managerPluginOpGet:
		return managerPluginOpGetID, nil
	case managerPluginOpHTTPForward:
		return managerPluginOpHTTPForwardID, nil
	default:
		return 0, fmt.Errorf("internalv2/access/node: unknown manager plugin op %q", op)
	}
}

func managerPluginOpFromID(id byte) (string, error) {
	switch id {
	case managerPluginOpListID:
		return managerPluginOpList, nil
	case managerPluginOpGetID:
		return managerPluginOpGet, nil
	case managerPluginOpHTTPForwardID:
		return managerPluginOpHTTPForward, nil
	default:
		return "", fmt.Errorf("internalv2/access/node: unknown manager plugin op id %d", id)
	}
}
