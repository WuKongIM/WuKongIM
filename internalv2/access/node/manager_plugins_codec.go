package node

import (
	"fmt"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
)

var (
	managerPluginRequestMagic  = [...]byte{'W', 'K', 'V', 'J', 1}
	managerPluginResponseMagic = [...]byte{'W', 'K', 'V', 'j', 1}
)

const (
	managerPluginOpList = "list_plugins"
	managerPluginOpGet  = "get_plugin"

	managerPluginOpListID byte = iota + 1
	managerPluginOpGetID

	maxManagerPluginRPCCollectionLen = 4096
)

type managerPluginRPCRequest struct {
	Op       string
	NodeID   uint64
	PluginNo string
}

type managerPluginRPCResponse struct {
	Status  string
	Plugins []managementusecase.Plugin
	Plugin  managementusecase.Plugin
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
	if offset != len(body) {
		return managerPluginRPCRequest{}, fmt.Errorf("internalv2/access/node: trailing manager plugin request bytes")
	}
	return managerPluginRPCRequest{Op: op, NodeID: nodeID, PluginNo: pluginNo}, nil
}

func encodeManagerPluginResponse(resp managerPluginRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, 256)
	dst = append(dst, managerPluginResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendManagerPlugins(dst, resp.Plugins)
	dst = appendManagerPlugin(dst, resp.Plugin)
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
	if offset != len(body) {
		return managerPluginRPCResponse{}, fmt.Errorf("internalv2/access/node: trailing manager plugin response bytes")
	}
	return resp, nil
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
	default:
		return "", fmt.Errorf("internalv2/access/node: unknown manager plugin op id %d", id)
	}
}
