package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/gin-gonic/gin"
)

const defaultPluginRouteMaxBodyBytes int64 = 10 << 20

func (s *Server) handlePluginRoute(c *gin.Context) {
	if s == nil || s.pluginRoutes == nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, defaultPluginRouteMaxBodyBytes)
	reqBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			c.String(http.StatusRequestEntityTooLarge, fmt.Sprintf("payload too large: max %d bytes", maxBytesErr.Limit))
			return
		}
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pluginReq := &pluginproto.HttpRequest{
		Method:  c.Request.Method,
		Path:    c.Param("path"),
		Headers: firstHeaderValues(c.Request.Header),
		Query:   firstQueryValues(c.Request.URL.Query()),
		Body:    reqBody,
	}
	dropHTTPHopByHopHeaders(pluginReq.Headers)
	resp, err := s.pluginRoutes.Route(c.Request.Context(), c.Param("plugin"), pluginReq)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	writePluginHTTPResponse(c, resp)
}

func firstHeaderValues(values http.Header) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		if len(value) == 0 {
			continue
		}
		out[key] = value[0]
	}
	return out
}

func clonePluginHeaders(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func firstQueryValues(values map[string][]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		if len(value) == 0 {
			continue
		}
		out[key] = value[0]
	}
	return out
}

func writePluginHTTPResponse(c *gin.Context, resp *pluginproto.HttpResponse) {
	if resp == nil {
		c.Status(http.StatusNoContent)
		return
	}
	headers := clonePluginHeaders(resp.GetHeaders())
	dropHTTPHopByHopHeaders(headers)
	for key, value := range headers {
		c.Writer.Header().Set(key, value)
	}
	status := int(resp.GetStatus())
	if status < 100 || status > 999 {
		status = http.StatusOK
	}
	c.Status(status)
	if len(resp.GetBody()) == 0 {
		return
	}
	if _, err := c.Writer.Write(resp.GetBody()); err != nil && c.Error(err) != nil {
		return
	}
}

func dropHTTPHopByHopHeaders(headers map[string]string) {
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
		if isHTTPHopByHopHeader(key) {
			delete(headers, key)
		}
	}
	for _, token := range connectionTokens {
		deleteHeaderCaseInsensitive(headers, strings.TrimSpace(token))
	}
}

func isHTTPHopByHopHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
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

func isPluginRouteRequest(path string) bool {
	return path == "/plugins" || strings.HasPrefix(path, "/plugins/")
}
