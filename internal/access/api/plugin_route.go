package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/gin-gonic/gin"
)

// PluginHTTPRouter invokes an installed plugin through the existing bounded
// plugin usecase. The HTTP adapter never manages plugin processes or storage.
type PluginHTTPRouter interface {
	Route(context.Context, string, *pluginproto.HttpRequest) (*pluginproto.HttpResponse, error)
}

const pluginHTTPMaxBodyBytes int64 = 10 << 20

// handlePluginRoute preserves the v2 business-backend route with bounded body
// and execution time. It inherits the product API's maintenance and network boundary.
func (s *Server) handlePluginRoute(c *gin.Context) {
	if s.plugins == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "plugin runtime unavailable"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, pluginHTTPMaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "plugin request too large"})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid plugin request"})
		}
		return
	}
	headers := make(map[string]string, len(c.Request.Header))
	for name, values := range c.Request.Header {
		if len(values) > 0 {
			headers[name] = values[0]
		}
	}
	query := make(map[string]string)
	for name, values := range c.Request.URL.Query() {
		if len(values) > 0 {
			query[name] = values[0]
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), s.pluginTimeout)
	defer cancel()
	response, err := s.plugins.Route(ctx, c.Param("plugin_no"), &pluginproto.HttpRequest{Method: c.Request.Method, Path: c.Param("path"), Headers: headers, Query: query, Body: body})
	if err != nil || response == nil || response.Status < 200 || response.Status > 599 || int64(len(response.Body)) > pluginHTTPMaxBodyBytes {
		c.JSON(http.StatusBadGateway, gin.H{"error": "plugin route failed"})
		return
	}
	// Plugin responses describe an HTTP payload, not a transport upgrade or
	// chunked stream. Let net/http frame the returned bytes itself.
	excluded := map[string]bool{"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true, "Proxy-Authorization": true, "Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true, "Content-Length": true}
	for name, value := range response.Headers {
		if http.CanonicalHeaderKey(name) == "Connection" {
			for _, token := range strings.Split(value, ",") {
				excluded[http.CanonicalHeaderKey(strings.TrimSpace(token))] = true
			}
		}
	}
	for name, value := range response.Headers {
		if !excluded[http.CanonicalHeaderKey(name)] {
			c.Header(name, value)
		}
	}
	c.Data(int(response.Status), c.Writer.Header().Get("Content-Type"), response.Body)
}
