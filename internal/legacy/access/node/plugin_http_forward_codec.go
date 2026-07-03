package node

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

var (
	pluginHTTPForwardRequestMagic  = [...]byte{'W', 'K', 'P', 'H', 1}
	pluginHTTPForwardResponseMagic = [...]byte{'W', 'K', 'P', 'R', 1}
)

const (
	maxPluginHTTPForwardBodyBytes   = 10 << 20
	maxPluginHTTPForwardHeaderBytes = 64 << 10
	maxPluginHTTPForwardMapEntries  = 256
)

func encodePluginHTTPForwardRequest(req pluginHTTPForwardRequest) ([]byte, error) {
	if req.Request == nil {
		req.Request = &pluginproto.HttpRequest{}
	}
	if err := validatePluginHTTPPayload(req.Request.GetHeaders(), req.Request.GetQuery(), req.Request.GetBody()); err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(pluginHTTPForwardRequestMagic)+len(req.PluginNo)+len(req.Request.GetPath())+len(req.Request.GetBody())+64)
	dst = append(dst, pluginHTTPForwardRequestMagic[:]...)
	dst = appendString(dst, req.PluginNo)
	dst = appendPluginHTTPRequest(dst, req.Request)
	return dst, nil
}

func decodePluginHTTPForwardRequest(body []byte) (pluginHTTPForwardRequest, error) {
	if !hasMagic(body, pluginHTTPForwardRequestMagic[:]) {
		return pluginHTTPForwardRequest{}, fmt.Errorf("access/node: invalid plugin http forward request codec")
	}
	offset := len(pluginHTTPForwardRequestMagic)
	pluginNo, next, err := readString(body, offset)
	if err != nil {
		return pluginHTTPForwardRequest{}, err
	}
	httpReq, next, err := readPluginHTTPRequest(body, next)
	if err != nil {
		return pluginHTTPForwardRequest{}, err
	}
	if next != len(body) {
		return pluginHTTPForwardRequest{}, fmt.Errorf("access/node: trailing plugin http forward request bytes")
	}
	if err := validatePluginHTTPPayload(httpReq.GetHeaders(), httpReq.GetQuery(), httpReq.GetBody()); err != nil {
		return pluginHTTPForwardRequest{}, err
	}
	return pluginHTTPForwardRequest{PluginNo: pluginNo, Request: httpReq}, nil
}

func encodePluginHTTPForwardResponse(resp pluginHTTPForwardResponse) ([]byte, error) {
	if resp.Response == nil {
		resp.Response = &pluginproto.HttpResponse{}
	}
	if err := validatePluginHTTPPayload(resp.Response.GetHeaders(), nil, resp.Response.GetBody()); err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(pluginHTTPForwardResponseMagic)+len(resp.Status)+len(resp.Error)+len(resp.Response.GetBody())+64)
	dst = append(dst, pluginHTTPForwardResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendString(dst, resp.Error)
	dst = appendPluginHTTPResponse(dst, resp.Response)
	return dst, nil
}

func decodePluginHTTPForwardResponse(body []byte) (pluginHTTPForwardResponse, error) {
	if !hasMagic(body, pluginHTTPForwardResponseMagic[:]) {
		return pluginHTTPForwardResponse{}, fmt.Errorf("access/node: invalid plugin http forward response codec")
	}
	offset := len(pluginHTTPForwardResponseMagic)
	status, next, err := readString(body, offset)
	if err != nil {
		return pluginHTTPForwardResponse{}, err
	}
	errText, next, err := readString(body, next)
	if err != nil {
		return pluginHTTPForwardResponse{}, err
	}
	httpResp, next, err := readPluginHTTPResponse(body, next)
	if err != nil {
		return pluginHTTPForwardResponse{}, err
	}
	if next != len(body) {
		return pluginHTTPForwardResponse{}, fmt.Errorf("access/node: trailing plugin http forward response bytes")
	}
	if err := validatePluginHTTPPayload(httpResp.GetHeaders(), nil, httpResp.GetBody()); err != nil {
		return pluginHTTPForwardResponse{}, err
	}
	return pluginHTTPForwardResponse{Status: status, Error: errText, Response: httpResp}, nil
}

func appendPluginHTTPRequest(dst []byte, req *pluginproto.HttpRequest) []byte {
	dst = appendString(dst, req.GetMethod())
	dst = appendString(dst, req.GetPath())
	dst = appendStringMap(dst, req.GetHeaders())
	dst = appendStringMap(dst, req.GetQuery())
	return appendBytes(dst, req.GetBody())
}

func readPluginHTTPRequest(body []byte, offset int) (*pluginproto.HttpRequest, int, error) {
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

func appendPluginHTTPResponse(dst []byte, resp *pluginproto.HttpResponse) []byte {
	dst = appendUvarint(dst, uint64(resp.GetStatus()))
	dst = appendStringMap(dst, resp.GetHeaders())
	return appendBytes(dst, resp.GetBody())
}

func readPluginHTTPResponse(body []byte, offset int) (*pluginproto.HttpResponse, int, error) {
	status, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	headers, next, err := readStringMap(body, next, "plugin http response headers")
	if err != nil {
		return nil, offset, err
	}
	respBody, next, err := readBytes(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &pluginproto.HttpResponse{Status: int32(status), Headers: headers, Body: respBody}, next, nil
}

func appendStringMap(dst []byte, values map[string]string) []byte {
	dst = appendUvarint(dst, uint64(len(values)))
	for key, value := range values {
		dst = appendString(dst, key)
		dst = appendString(dst, value)
	}
	return dst
}

func readStringMap(body []byte, offset int, label string) (map[string]string, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if count > maxPluginHTTPForwardMapEntries {
		return nil, offset, fmt.Errorf("access/node: %s count too large", label)
	}
	values := make(map[string]string, int(count))
	offset = next
	for i := 0; i < int(count); i++ {
		var key, value string
		if key, offset, err = readString(body, offset); err != nil {
			return nil, offset, err
		}
		if value, offset, err = readString(body, offset); err != nil {
			return nil, offset, err
		}
		values[key] = value
	}
	if len(values) == 0 {
		return nil, offset, nil
	}
	return values, offset, nil
}

func validatePluginHTTPPayload(headers, query map[string]string, body []byte) error {
	if len(body) > maxPluginHTTPForwardBodyBytes {
		return fmt.Errorf("access/node: plugin http body too large: %d > %d", len(body), maxPluginHTTPForwardBodyBytes)
	}
	if len(headers) > maxPluginHTTPForwardMapEntries {
		return fmt.Errorf("access/node: plugin http headers count too large")
	}
	if len(query) > maxPluginHTTPForwardMapEntries {
		return fmt.Errorf("access/node: plugin http query count too large")
	}
	if pluginHTTPMapBytes(headers)+pluginHTTPMapBytes(query) > maxPluginHTTPForwardHeaderBytes {
		return fmt.Errorf("access/node: plugin http headers too large")
	}
	return nil
}

func pluginHTTPMapBytes(values map[string]string) int {
	total := 0
	for key, value := range values {
		total += len(key) + len(value)
	}
	return total
}
