package plugin

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

const defaultHTTPForwardMaxBodyBytes int64 = 10 << 20
const defaultHTTPForwardMaxHeaderBytes int64 = 64 << 10

// HTTPForward handles PDK-compatible /plugin/httpForward host RPCs.
func (a *App) HTTPForward(ctx context.Context, req *pluginproto.ForwardHttpReq, callerUID string) (*pluginproto.HttpResponse, error) {
	if req == nil {
		req = &pluginproto.ForwardHttpReq{}
	}
	normalized, err := a.normalizeHTTPForwardRequest(req, callerUID)
	if err != nil {
		return nil, err
	}
	switch {
	case normalized.GetToNodeId() == -1:
		return nil, ErrHTTPForwardFanoutDeferred
	case normalized.GetToNodeId() > 0:
		if a == nil || a.httpForwarder == nil {
			return nil, ErrHTTPForwarderRequired
		}
		resp, err := a.httpForwarder.ForwardPluginHTTP(ctx, uint64(normalized.GetToNodeId()), normalized)
		if err != nil {
			return nil, err
		}
		if err := a.validateHTTPForwardResponseSize(resp); err != nil {
			return nil, err
		}
		return clonePluginHTTPResponse(resp), nil
	default:
		if a == nil {
			return nil, ErrInvokerRequired
		}
		return a.Route(ctx, normalized.GetPluginNo(), normalized.GetRequest())
	}
}

func (a *App) normalizeHTTPForwardRequest(req *pluginproto.ForwardHttpReq, callerUID string) (*pluginproto.ForwardHttpReq, error) {
	pluginNo := strings.TrimSpace(req.GetPluginNo())
	if pluginNo == "" {
		pluginNo = strings.TrimSpace(callerUID)
	}
	if pluginNo == "" {
		return nil, ErrPluginNoRequired
	}
	httpReq := clonePluginHTTPRequest(req.GetRequest())
	dropHopByHopHeaders(httpReq.Headers)
	if err := a.validateHTTPForwardRequestSize(httpReq); err != nil {
		return nil, err
	}
	return &pluginproto.ForwardHttpReq{
		PluginNo: pluginNo,
		ToNodeId: req.GetToNodeId(),
		Request:  httpReq,
	}, nil
}

func (a *App) validateHTTPForwardRequestSize(req *pluginproto.HttpRequest) error {
	if req == nil {
		req = &pluginproto.HttpRequest{}
	}
	if err := validateHTTPBodySize(req.GetBody(), a.httpForwardBodyLimit()); err != nil {
		return err
	}
	if int64(pluginHTTPHeaderBytes(req.GetHeaders())+pluginHTTPHeaderBytes(req.GetQuery())) > defaultHTTPForwardMaxHeaderBytes {
		return ErrHTTPForwardHeaderTooLarge
	}
	return nil
}

func (a *App) validateHTTPForwardResponseSize(resp *pluginproto.HttpResponse) error {
	if resp == nil {
		return nil
	}
	if err := validateHTTPBodySize(resp.GetBody(), a.httpForwardBodyLimit()); err != nil {
		return err
	}
	if int64(pluginHTTPHeaderBytes(resp.GetHeaders())) > defaultHTTPForwardMaxHeaderBytes {
		return ErrHTTPForwardHeaderTooLarge
	}
	return nil
}

func (a *App) httpForwardBodyLimit() int64 {
	if a != nil && a.httpForwardLimit > 0 {
		return a.httpForwardLimit
	}
	return defaultHTTPForwardMaxBodyBytes
}

func validateHTTPBodySize(body []byte, limit int64) error {
	if int64(len(body)) > limit {
		return fmt.Errorf("%w: %d > %d", ErrHTTPForwardBodyTooLarge, len(body), limit)
	}
	return nil
}

func clonePluginHTTPRequest(req *pluginproto.HttpRequest) *pluginproto.HttpRequest {
	if req == nil {
		req = &pluginproto.HttpRequest{}
	}
	return &pluginproto.HttpRequest{
		Method:  req.GetMethod(),
		Path:    req.GetPath(),
		Headers: cloneStringMap(req.GetHeaders()),
		Query:   cloneStringMap(req.GetQuery()),
		Body:    append([]byte(nil), req.GetBody()...),
	}
}

func clonePluginHTTPResponse(resp *pluginproto.HttpResponse) *pluginproto.HttpResponse {
	if resp == nil {
		return &pluginproto.HttpResponse{}
	}
	return &pluginproto.HttpResponse{
		Status:  resp.GetStatus(),
		Headers: cloneStringMap(resp.GetHeaders()),
		Body:    append([]byte(nil), resp.GetBody()...),
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func dropHopByHopHeaders(headers map[string]string) {
	if len(headers) == 0 {
		return
	}
	var connectionTokens []string
	for key, value := range headers {
		if http.CanonicalHeaderKey(key) == "Connection" {
			connectionTokens = append(connectionTokens, strings.Split(value, ",")...)
		}
	}
	for key := range headers {
		if isHopByHopHeader(key) {
			delete(headers, key)
		}
	}
	for _, token := range connectionTokens {
		deleteHeaderCaseInsensitive(headers, strings.TrimSpace(token))
	}
}

func deleteHeaderCaseInsensitive(headers map[string]string, key string) {
	if key == "" {
		return
	}
	canonical := http.CanonicalHeaderKey(key)
	for existing := range headers {
		if http.CanonicalHeaderKey(existing) == canonical {
			delete(headers, existing)
		}
	}
}

func isHopByHopHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

func pluginHTTPHeaderBytes(values map[string]string) int {
	total := 0
	for key, value := range values {
		total += len(key) + len(value)
	}
	return total
}
