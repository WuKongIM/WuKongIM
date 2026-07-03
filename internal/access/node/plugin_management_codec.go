package node

import (
	"encoding/json"
	"fmt"
	"time"

	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

var (
	pluginManagementRequestMagic  = [...]byte{'W', 'K', 'P', 'M', 1}
	pluginManagementResponseMagic = [...]byte{'W', 'K', 'P', 'N', 1}
)

const (
	maxPluginManagementConfigBytes   = 1 << 20
	maxPluginManagementRequestBytes  = maxPluginManagementConfigBytes + 8<<10
	maxPluginManagementResponseBytes = 10 << 20
	maxPluginManagementStringBytes   = 1024
	maxPluginManagementPlugins       = 512
	maxPluginManagementMethods       = 64
)

func encodePluginManagementRequest(req pluginManagementRequest) ([]byte, error) {
	if len(req.Config) > maxPluginManagementConfigBytes {
		return nil, fmt.Errorf("access/node: plugin management config too large")
	}
	if err := validatePluginManagementString("op", req.Op); err != nil {
		return nil, err
	}
	if err := validatePluginManagementString("plugin_no", req.PluginNo); err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(pluginManagementRequestMagic)+len(req.PluginNo)+len(req.Config)+32)
	dst = append(dst, pluginManagementRequestMagic[:]...)
	dst = appendString(dst, req.Op)
	dst = appendUvarint(dst, req.NodeID)
	dst = appendString(dst, req.PluginNo)
	dst = appendBytes(dst, req.Config)
	if len(dst) > maxPluginManagementRequestBytes {
		return nil, fmt.Errorf("access/node: plugin management request too large")
	}
	return dst, nil
}

func decodePluginManagementRequest(body []byte) (pluginManagementRequest, error) {
	if len(body) > maxPluginManagementRequestBytes {
		return pluginManagementRequest{}, fmt.Errorf("access/node: plugin management request too large")
	}
	if !hasMagic(body, pluginManagementRequestMagic[:]) {
		return pluginManagementRequest{}, fmt.Errorf("access/node: invalid plugin management request codec")
	}
	offset := len(pluginManagementRequestMagic)
	op, next, err := readPluginManagementString(body, offset, "op")
	if err != nil {
		return pluginManagementRequest{}, err
	}
	nodeID, next, err := readUvarint(body, next)
	if err != nil {
		return pluginManagementRequest{}, err
	}
	pluginNo, next, err := readPluginManagementString(body, next, "plugin_no")
	if err != nil {
		return pluginManagementRequest{}, err
	}
	config, next, err := readBytes(body, next)
	if err != nil {
		return pluginManagementRequest{}, err
	}
	if next != len(body) {
		return pluginManagementRequest{}, fmt.Errorf("access/node: trailing plugin management request bytes")
	}
	if len(config) > maxPluginManagementConfigBytes {
		return pluginManagementRequest{}, fmt.Errorf("access/node: plugin management config too large")
	}
	return pluginManagementRequest{Op: op, NodeID: nodeID, PluginNo: pluginNo, Config: append(json.RawMessage(nil), config...)}, nil
}

func validatePluginManagementString(label, value string) error {
	if len(value) > maxPluginManagementStringBytes {
		return fmt.Errorf("access/node: plugin management %s too large", label)
	}
	return nil
}

func readPluginManagementString(body []byte, offset int, label string) (string, int, error) {
	length, next, err := readUvarint(body, offset)
	if err != nil {
		return "", offset, err
	}
	if length > maxPluginManagementStringBytes {
		return "", offset, fmt.Errorf("access/node: plugin management %s too large", label)
	}
	offset = next
	end := offset + int(length)
	if end < offset || end > len(body) {
		return "", offset, fmt.Errorf("access/node: short bytes")
	}
	return string(body[offset:end]), end, nil
}

func encodePluginManagementResponse(resp pluginManagementResponse) ([]byte, error) {
	dst := make([]byte, 0, len(pluginManagementResponseMagic)+256)
	dst = append(dst, pluginManagementResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendString(dst, resp.ErrorCode)
	dst = appendString(dst, resp.Error)
	var err error
	dst, err = appendPluginManagementList(dst, resp.List)
	if err != nil {
		return nil, err
	}
	dst, err = appendPluginManagementDetail(dst, resp.Detail)
	if err != nil {
		return nil, err
	}
	if len(dst) > maxPluginManagementResponseBytes {
		return nil, fmt.Errorf("access/node: plugin management response too large")
	}
	return dst, nil
}

func decodePluginManagementResponse(body []byte) (pluginManagementResponse, error) {
	if len(body) > maxPluginManagementResponseBytes {
		return pluginManagementResponse{}, fmt.Errorf("access/node: plugin management response too large")
	}
	if !hasMagic(body, pluginManagementResponseMagic[:]) {
		return pluginManagementResponse{}, fmt.Errorf("access/node: invalid plugin management response codec")
	}
	offset := len(pluginManagementResponseMagic)
	status, next, err := readString(body, offset)
	if err != nil {
		return pluginManagementResponse{}, err
	}
	errorCode, next, err := readString(body, next)
	if err != nil {
		return pluginManagementResponse{}, err
	}
	errText, next, err := readString(body, next)
	if err != nil {
		return pluginManagementResponse{}, err
	}
	list, next, err := readPluginManagementList(body, next)
	if err != nil {
		return pluginManagementResponse{}, err
	}
	detail, next, err := readPluginManagementDetail(body, next)
	if err != nil {
		return pluginManagementResponse{}, err
	}
	if next != len(body) {
		return pluginManagementResponse{}, fmt.Errorf("access/node: trailing plugin management response bytes")
	}
	return pluginManagementResponse{Status: status, ErrorCode: errorCode, Error: errText, List: list, Detail: detail}, nil
}

func appendPluginManagementList(dst []byte, list *pluginusecase.LocalPluginList) ([]byte, error) {
	if list == nil {
		return appendPluginManagementBool(dst, false), nil
	}
	if len(list.Plugins) > maxPluginManagementPlugins {
		return nil, fmt.Errorf("access/node: plugin management plugin count too large")
	}
	dst = appendPluginManagementBool(dst, true)
	dst = appendUvarint(dst, list.NodeID)
	dst = appendUvarint(dst, uint64(len(list.Plugins)))
	for _, item := range list.Plugins {
		var err error
		dst, err = appendPluginManagementPlugin(dst, item)
		if err != nil {
			return nil, err
		}
	}
	return dst, nil
}

func readPluginManagementList(body []byte, offset int) (*pluginusecase.LocalPluginList, int, error) {
	present, next, err := readPluginManagementBool(body, offset)
	if err != nil || !present {
		return nil, next, err
	}
	nodeID, next, err := readUvarint(body, next)
	if err != nil {
		return nil, offset, err
	}
	count, next, err := readUvarint(body, next)
	if err != nil {
		return nil, offset, err
	}
	if count > maxPluginManagementPlugins {
		return nil, offset, fmt.Errorf("access/node: plugin management plugin count too large")
	}
	plugins := make([]pluginusecase.LocalPlugin, int(count))
	for i := range plugins {
		plugins[i], next, err = readPluginManagementPlugin(body, next)
		if err != nil {
			return nil, offset, err
		}
	}
	return &pluginusecase.LocalPluginList{NodeID: nodeID, Plugins: plugins}, next, nil
}

func appendPluginManagementDetail(dst []byte, detail *pluginusecase.LocalPluginDetail) ([]byte, error) {
	if detail == nil {
		return appendPluginManagementBool(dst, false), nil
	}
	dst = appendPluginManagementBool(dst, true)
	return appendPluginManagementPlugin(dst, *detail)
}

func readPluginManagementDetail(body []byte, offset int) (*pluginusecase.LocalPluginDetail, int, error) {
	present, next, err := readPluginManagementBool(body, offset)
	if err != nil || !present {
		return nil, next, err
	}
	plugin, next, err := readPluginManagementPlugin(body, next)
	if err != nil {
		return nil, offset, err
	}
	detail := pluginusecase.LocalPluginDetail(plugin)
	return &detail, next, nil
}

func appendPluginManagementPlugin(dst []byte, plugin pluginusecase.LocalPlugin) ([]byte, error) {
	if plugin.Priority < 0 || plugin.PID < 0 {
		return nil, fmt.Errorf("access/node: plugin management negative integer field")
	}
	if len(plugin.Methods) > maxPluginManagementMethods {
		return nil, fmt.Errorf("access/node: plugin management method count too large")
	}
	templateRaw, err := marshalPluginManagementConfigTemplate(plugin.ConfigTemplate)
	if err != nil {
		return nil, err
	}
	configRaw, err := marshalPluginManagementConfig(plugin.Config)
	if err != nil {
		return nil, err
	}
	dst = appendUvarint(dst, plugin.NodeID)
	dst = appendString(dst, plugin.No)
	dst = appendString(dst, plugin.Name)
	dst = appendString(dst, plugin.Version)
	dst = appendBytes(dst, templateRaw)
	dst = appendBytes(dst, configRaw)
	dst = appendPluginManagementOptionalTime(dst, plugin.CreatedAt)
	dst = appendPluginManagementOptionalTime(dst, plugin.UpdatedAt)
	dst = appendString(dst, string(plugin.Status))
	dst = appendPluginManagementBool(dst, plugin.Enabled)
	dst, err = appendPluginManagementMethods(dst, plugin.Methods)
	if err != nil {
		return nil, err
	}
	dst = appendUvarint(dst, uint64(plugin.Priority))
	dst = appendPluginManagementBool(dst, plugin.PersistAfterSync)
	dst = appendPluginManagementBool(dst, plugin.ReplySync)
	dst = appendUvarint(dst, uint64(plugin.IsAI))
	dst = appendUvarint(dst, uint64(plugin.PID))
	dst = appendPluginManagementTime(dst, plugin.LastSeenAt)
	dst = appendString(dst, plugin.LastError)
	return dst, nil
}

func readPluginManagementPlugin(body []byte, offset int) (pluginusecase.LocalPlugin, int, error) {
	var item pluginusecase.LocalPlugin
	var err error
	if item.NodeID, offset, err = readUvarint(body, offset); err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	if item.No, offset, err = readString(body, offset); err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	if item.Name, offset, err = readString(body, offset); err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	if item.Version, offset, err = readString(body, offset); err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	templateRaw, next, err := readBytes(body, offset)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	item.ConfigTemplate, err = unmarshalPluginManagementConfigTemplate(templateRaw)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	configRaw, next, err := readBytes(body, next)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	item.Config, err = unmarshalPluginManagementConfig(configRaw)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	offset = next
	if item.CreatedAt, offset, err = readPluginManagementOptionalTime(body, offset); err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	if item.UpdatedAt, offset, err = readPluginManagementOptionalTime(body, offset); err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	status, next, err := readString(body, offset)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	item.Status = pluginusecase.Status(status)
	item.Enabled, next, err = readPluginManagementBool(body, next)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	item.Methods, next, err = readPluginManagementMethods(body, next)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	priority, next, err := readUvarint(body, next)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	if priority > maxPluginManagementInt() {
		return pluginusecase.LocalPlugin{}, offset, fmt.Errorf("access/node: plugin management priority too large")
	}
	item.Priority = int(priority)
	item.PersistAfterSync, next, err = readPluginManagementBool(body, next)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	item.ReplySync, next, err = readPluginManagementBool(body, next)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	isAI, next, err := readUvarint(body, next)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	if isAI > uint64(^uint8(0)) {
		return pluginusecase.LocalPlugin{}, offset, fmt.Errorf("access/node: plugin management is_ai too large")
	}
	item.IsAI = uint8(isAI)
	pid, next, err := readUvarint(body, next)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	if pid > maxPluginManagementInt() {
		return pluginusecase.LocalPlugin{}, offset, fmt.Errorf("access/node: plugin management pid too large")
	}
	item.PID = int(pid)
	item.LastSeenAt, next, err = readPluginManagementTime(body, next)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	item.LastError, next, err = readString(body, next)
	if err != nil {
		return pluginusecase.LocalPlugin{}, offset, err
	}
	return item, next, nil
}

func marshalPluginManagementConfigTemplate(template *pluginproto.ConfigTemplate) ([]byte, error) {
	if template == nil {
		return nil, nil
	}
	raw, err := template.Marshal()
	if err != nil {
		return nil, err
	}
	if len(raw) > maxPluginManagementConfigBytes {
		return nil, fmt.Errorf("access/node: plugin management config template too large")
	}
	return raw, nil
}

func unmarshalPluginManagementConfigTemplate(raw []byte) (*pluginproto.ConfigTemplate, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > maxPluginManagementConfigBytes {
		return nil, fmt.Errorf("access/node: plugin management config template too large")
	}
	var template pluginproto.ConfigTemplate
	if err := template.Unmarshal(raw); err != nil {
		return nil, err
	}
	return &template, nil
}

func marshalPluginManagementConfig(config map[string]any) ([]byte, error) {
	if config == nil {
		return nil, nil
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxPluginManagementConfigBytes {
		return nil, fmt.Errorf("access/node: plugin management config too large")
	}
	return raw, nil
}

func unmarshalPluginManagementConfig(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > maxPluginManagementConfigBytes {
		return nil, fmt.Errorf("access/node: plugin management config too large")
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	return config, nil
}

func appendPluginManagementMethods(dst []byte, methods []pluginusecase.Method) ([]byte, error) {
	if len(methods) > maxPluginManagementMethods {
		return nil, fmt.Errorf("access/node: plugin management method count too large")
	}
	dst = appendUvarint(dst, uint64(len(methods)))
	for _, method := range methods {
		dst = appendString(dst, string(method))
	}
	return dst, nil
}

func readPluginManagementMethods(body []byte, offset int) ([]pluginusecase.Method, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if count > maxPluginManagementMethods {
		return nil, offset, fmt.Errorf("access/node: plugin management method count too large")
	}
	methods := make([]pluginusecase.Method, int(count))
	offset = next
	for i := range methods {
		var method string
		method, offset, err = readString(body, offset)
		if err != nil {
			return nil, offset, err
		}
		methods[i] = pluginusecase.Method(method)
	}
	return methods, offset, nil
}

func appendPluginManagementOptionalTime(dst []byte, value *time.Time) []byte {
	if value == nil {
		return appendPluginManagementBool(dst, false)
	}
	dst = appendPluginManagementBool(dst, true)
	return appendPluginManagementTime(dst, *value)
}

func readPluginManagementOptionalTime(body []byte, offset int) (*time.Time, int, error) {
	present, next, err := readPluginManagementBool(body, offset)
	if err != nil || !present {
		return nil, next, err
	}
	value, next, err := readPluginManagementTime(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &value, next, nil
}

func appendPluginManagementTime(dst []byte, value time.Time) []byte {
	if value.IsZero() {
		return appendUvarint(dst, 0)
	}
	return appendUvarint(dst, uint64(value.UTC().UnixNano()))
}

func readPluginManagementTime(body []byte, offset int) (time.Time, int, error) {
	nano, next, err := readUvarint(body, offset)
	if err != nil {
		return time.Time{}, offset, err
	}
	if nano == 0 {
		return time.Time{}, next, nil
	}
	if nano > uint64(1<<63-1) {
		return time.Time{}, offset, fmt.Errorf("access/node: plugin management timestamp too large")
	}
	return time.Unix(0, int64(nano)).UTC(), next, nil
}

func maxPluginManagementInt() uint64 {
	return uint64(^uint(0) >> 1)
}

func appendPluginManagementBool(dst []byte, value bool) []byte {
	if value {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func readPluginManagementBool(body []byte, offset int) (bool, int, error) {
	if offset >= len(body) {
		return false, offset, fmt.Errorf("access/node: short plugin management bool")
	}
	switch body[offset] {
	case 0:
		return false, offset + 1, nil
	case 1:
		return true, offset + 1, nil
	default:
		return false, offset, fmt.Errorf("access/node: invalid plugin management bool")
	}
}
