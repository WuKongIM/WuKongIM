package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	cluster "github.com/WuKongIM/WuKongIM/pkg/cluster/clusterserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/version"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

type ManagerServer struct {
	s *Server
	r *wkhttp.WKHttp
	wklog.Log
	addr string
}

func NewManagerServer(s *Server) *ManagerServer {
	r := wkhttp.New()

	return &ManagerServer{
		addr: s.opts.Monitor.Addr,
		s:    s,
		r:    r,
		Log:  wklog.NewWKLog("ManagerServer"),
	}

}

func (m *ManagerServer) Start() {

	m.r.Use(wkhttp.CORSMiddleware())
	// jwt和token认证中间件
	m.r.Use(m.jwtAndTokenAuthMiddleware())

	m.r.Use(func(c *wkhttp.Context) { // 管理者权限判断
		if c.FullPath() == "/api/varz" {
			c.Next()
			return
		}
		if strings.TrimSpace(m.s.opts.ManagerToken) == "" {
			c.Next()
			return
		}
		if strings.HasPrefix(c.FullPath(), "/web") {
			c.Next()
			return
		}

		managerToken := c.GetHeader("token")
		if managerToken != m.s.opts.ManagerToken {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})

	m.r.GetGinRoute().Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{"/metrics"})))

	st, _ := fs.Sub(version.WebFs, "web/dist")
	m.r.GetGinRoute().NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/web") {
			c.FileFromFS("./index.html", http.FS(st))
			return
		}
	})

	m.r.GetGinRoute().StaticFS("/web", http.FS(st))

	m.setRoutes()

	go func() {
		err := m.r.Run(m.addr) // listen and serve
		if err != nil {
			panic(err)
		}
	}()
	m.Info("ManagerServer started", zap.String("addr", m.addr))

	_, port := parseAddr(m.addr)
	m.Info(fmt.Sprintf("Manager web address： http://localhost:%d/web", port))
}

func (m *ManagerServer) Stop() error {

	return nil
}

func (m *ManagerServer) setRoutes() {

	// 监控收集
	metricHandler := m.s.trace.Handler()
	m.r.GET("/metrics", func(c *wkhttp.Context) {
		metricHandler.ServeHTTP(c.Writer, c.Request)
	})

	connz := NewConnzAPI(m.s)
	connz.Route(m.r)

	// varz := NewVarzAPI(s.s)
	// varz.Route(s.r)

	// 管理者api
	manager := NewManagerAPI(m.s)
	manager.Route(m.r)

	// // 系统api
	// system := NewSystemAPI(s.s)
	// system.Route(s.r)

	// 分布式api
	clusterServer, ok := m.s.cluster.(*cluster.Server)
	if ok {
		clusterServer.ServerAPI(m.r, "/cluster")
	}
	// 监控
	m.s.trace.Route(m.r)

}

func (m *ManagerServer) jwtAndTokenAuthMiddleware() wkhttp.HandlerFunc {
	return func(c *wkhttp.Context) {

		fpath := c.FullPath()
		if strings.HasPrefix(fpath, "/manager/login") { // 登录不需要认证
			c.Next()
			return
		}
		if strings.HasPrefix(fpath, "/web") {
			c.Next()
			return
		}

		// 管理token认证
		token := c.GetHeader("token")
		if strings.TrimSpace(token) != "" && token == m.s.opts.ManagerToken {
			c.Set("username", m.s.opts.ManagerUID)
			c.Next()
			return
		}

		// 认证jwt
		authorization := c.GetHeader("Authorization")
		if authorization == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header is required"})
			c.Abort()
			return
		}
		authorization = strings.TrimPrefix(authorization, "Bearer ")
		if authorization == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			c.Abort()
			return
		}

		jwtToken, err := jwt.ParseWithClaims(authorization, jwt.MapClaims{}, func(token *jwt.Token) (interface{}, error) {
			return []byte(m.s.opts.Jwt.Secret), nil
		})
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			c.Abort()
			return
		}

		if !jwtToken.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid jwt token"})
			c.Abort()
			return
		}
		mapCaims := jwtToken.Claims.(jwt.MapClaims)
		if mapCaims["username"] == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid jwt token, username is empty"})
			c.Abort()
			return
		}

		c.Set("username", mapCaims["username"])
		c.Next()
	}
}
