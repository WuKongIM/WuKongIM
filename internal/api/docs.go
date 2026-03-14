package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type docs struct {
	s *Server
	wklog.Log
}

func newDocs(s *Server) *docs {
	return &docs{
		s:   s,
		Log: wklog.NewWKLog("docs"),
	}
}

// route 配置文档路由
func (d *docs) route(r *wkhttp.WKHttp) {
	// 在生产模式下禁用文档端点以提高安全性和性能
	if options.G.Mode == options.ReleaseMode {
		d.Info("Documentation endpoints disabled in release mode for security")
		r.GET("/docs", d.disabledInRelease)              // 显示禁用消息
		r.GET("/docs/", d.disabledInRelease)             // 显示禁用消息
		r.GET("/docs/openapi.json", d.disabledInRelease) // 显示禁用消息
		r.GET("/docs/health", d.releaseHealthCheck)      // 简化的健康检查
		return
	}

	// 开发模式下启用完整的文档功能
	d.Info("Documentation endpoints enabled in development mode")
	r.GET("/docs", d.swaggerUI)            // Swagger UI 主页面
	r.GET("/docs/", d.redirectToDocs)      // 重定向 /docs/ 到 /docs
	r.GET("/docs/openapi.json", d.openAPI) // OpenAPI 规范文件
	r.GET("/docs/health", d.docsHealth)    // 文档服务健康检查
}

// redirectToDocs 重定向 /docs/ 到 /docs
func (d *docs) redirectToDocs(c *wkhttp.Context) {
	c.Redirect(http.StatusMovedPermanently, "/docs")
}

// docsHealth 文档服务健康检查
func (d *docs) docsHealth(c *wkhttp.Context) {
	c.JSON(http.StatusOK, map[string]interface{}{
		"status":      "ok",
		"service":     "docs",
		"description": "WuKongIM API Documentation Service",
		"endpoints": map[string]string{
			"swagger_ui":   "/docs",
			"openapi_spec": "/docs/openapi.json",
			"health_check": "/docs/health",
		},
	})
}

// disabledInRelease 在生产模式下显示禁用消息
func (d *docs) disabledInRelease(c *wkhttp.Context) {
	c.JSON(http.StatusForbidden, map[string]interface{}{
		"error":      "Documentation endpoints are disabled in release mode",
		"message":    "API documentation is only available in development mode for security reasons",
		"mode":       string(options.G.Mode),
		"suggestion": "To access documentation, run the server in debug mode or check the static documentation files",
	})
}

// releaseHealthCheck 生产模式下的简化健康检查
func (d *docs) releaseHealthCheck(c *wkhttp.Context) {
	c.JSON(http.StatusOK, map[string]interface{}{
		"status":      "disabled",
		"service":     "docs",
		"mode":        string(options.G.Mode),
		"description": "Documentation service is disabled in release mode",
	})
}

// swaggerUI 提供 Swagger UI 界面
func (d *docs) swaggerUI(c *wkhttp.Context) {
	// 生成 Swagger UI HTML
	html := d.generateSwaggerHTML()
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, html)
}

// openAPI 提供 OpenAPI 规范文件
func (d *docs) openAPI(c *wkhttp.Context) {
	// 尝试多个可能的 OpenAPI 文件路径
	possiblePaths := []string{
		filepath.Join("docs", "api", "openapi.json"),                          // 相对于工作目录
		filepath.Join(options.G.DataDir, "..", "docs", "api", "openapi.json"), // 相对于数据目录
		"./docs/api/openapi.json",                                              // 当前目录
	}

	var data []byte
	var err error
	var usedPath string

	for _, path := range possiblePaths {
		data, err = os.ReadFile(path)
		if err == nil {
			usedPath = path
			break
		}
	}

	if err != nil {
		d.Error("Failed to read openapi.json from any location", zap.Error(err), zap.Strings("tried_paths", possiblePaths))
		c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to load API specification. Please ensure docs/api/openapi.json exists.",
		})
		return
	}

	d.Debug("Successfully loaded OpenAPI spec", zap.String("path", usedPath))

	// 验证 JSON 格式
	var spec map[string]interface{}
	if err := json.Unmarshal(data, &spec); err != nil {
		d.Error("Invalid JSON in openapi.json", zap.Error(err), zap.String("path", usedPath))
		c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Invalid API specification format",
		})
		return
	}

	c.Header("Content-Type", "application/json")
	c.Header("Access-Control-Allow-Origin", "*") // Allow CORS for Swagger UI
	c.Data(http.StatusOK, "application/json", data)
}

// generateSwaggerHTML 生成 Swagger UI HTML 页面
func (d *docs) generateSwaggerHTML() string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>WuKongIM API Documentation</title>
    <link rel="stylesheet" type="text/css" href="https://unpkg.com/swagger-ui-dist@4.15.5/swagger-ui.css" />
    <link rel="icon" type="image/png" href="https://unpkg.com/swagger-ui-dist@4.15.5/favicon-32x32.png" sizes="32x32" />
    <link rel="icon" type="image/png" href="https://unpkg.com/swagger-ui-dist@4.15.5/favicon-16x16.png" sizes="16x16" />
    <style>
        html {
            box-sizing: border-box;
            overflow: -moz-scrollbars-vertical;
            overflow-y: scroll;
        }
        *, *:before, *:after {
            box-sizing: inherit;
        }
        body {
            margin:0;
            background: #fafafa;
        }
        .swagger-ui .topbar {
            background-color: #1b1b1b;
            padding: 10px 0;
        }
        .swagger-ui .topbar .download-url-wrapper {
            display: none;
        }
        .swagger-ui .topbar .link {
            content: "WuKongIM API Documentation";
        }
        .swagger-ui .info .title {
            color: #3b4151;
        }
        .custom-header {
            background: linear-gradient(90deg, #667eea 0%, #764ba2 100%);
            color: white;
            padding: 20px;
            text-align: center;
            margin-bottom: 20px;
        }
        .custom-header h1 {
            margin: 0;
            font-size: 2.5em;
            font-weight: 300;
        }
        .custom-header p {
            margin: 10px 0 0 0;
            font-size: 1.1em;
            opacity: 0.9;
        }
    </style>
</head>
<body>
    <div class="custom-header">
        <h1>🐒 WuKongIM API</h1>
        <p>High-Performance Instant Messaging System - REST API Documentation</p>
    </div>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@4.15.5/swagger-ui-bundle.js" charset="UTF-8"></script>
    <script src="https://unpkg.com/swagger-ui-dist@4.15.5/swagger-ui-standalone-preset.js" charset="UTF-8"></script>
    <script>
        window.onload = function() {
            // Begin Swagger UI call region
            const ui = SwaggerUIBundle({
                url: '/docs/openapi.json',
                dom_id: '#swagger-ui',
                deepLinking: true,
                presets: [
                    SwaggerUIBundle.presets.apis,
                    SwaggerUIStandalonePreset
                ],
                plugins: [
                    SwaggerUIBundle.plugins.DownloadUrl
                ],
                layout: "StandaloneLayout",
                validatorUrl: null,
                docExpansion: "list",
                defaultModelsExpandDepth: 1,
                defaultModelExpandDepth: 1,
                displayRequestDuration: true,
                tryItOutEnabled: true,
                filter: true,
                showExtensions: true,
                showCommonExtensions: true,
                supportedSubmitMethods: ['get', 'post', 'put', 'delete', 'patch', 'head', 'options'],
                onComplete: function() {
                    console.log('WuKongIM API Documentation loaded successfully');
                    // Add custom styling after load
                    const style = document.createElement('style');
                    style.textContent = '.swagger-ui .topbar-wrapper .link:after { content: "WuKongIM API v2.0"; }';
                    document.head.appendChild(style);
                },
                onFailure: function(data) {
                    console.error('Failed to load API specification:', data);
                    // Show user-friendly error message
                    document.getElementById('swagger-ui').innerHTML =
                        '<div style="padding: 40px; text-align: center; color: #721c24; background: #f8d7da; border: 1px solid #f5c6cb; border-radius: 4px; margin: 20px;">' +
                        '<h3>⚠️ Failed to Load API Documentation</h3>' +
                        '<p>Could not load the OpenAPI specification. Please ensure the server is running properly.</p>' +
                        '<p><strong>Error:</strong> ' + (data.message || 'Unknown error') + '</p>' +
                        '</div>';
                }
            });
            // End Swagger UI call region

            window.ui = ui;
        };
    </script>
</body>
</html>`
}
