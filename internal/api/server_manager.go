package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	cluster "github.com/WuKongIM/WuKongIM/pkg/cluster/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/version"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

type managerServer struct {
	s *Server
	r *wkhttp.WKHttp
	wklog.Log
	addr string
}

func newManagerServer(s *Server) *managerServer {
	// r := wkhttp.New()
	log := wklog.NewWKLog("managerServer")
	r := wkhttp.NewWithLogger(wkhttp.LoggerWithWklog(log))

	return &managerServer{
		addr: options.G.Manager.Addr,
		s:    s,
		r:    r,
		Log:  log,
	}

}

func (m *managerServer) start() {

	m.r.Use(wkhttp.CORSMiddleware())
	// jwt和token认证中间件
	m.r.Use(m.jwtAndTokenAuthMiddleware())

	m.r.GetGinRoute().Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{"/metrics"})))

	st, _ := fs.Sub(version.WebFs, "web/dist")
	m.r.GetGinRoute().NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/web") {
			c.FileFromFS("./", http.FS(st))
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

func (m *managerServer) stop() {

}

func (m *managerServer) setRoutes() {

	// 监控收集
	metricHandler := trace.GlobalTrace.Handler()
	m.r.GET("/metrics", func(c *wkhttp.Context) {
		metricHandler.ServeHTTP(c.Writer, c.Request)
	})

	connz := newConnz(m.s)
	connz.route(m.r)

	varz := newVarz(m.s)
	varz.route(m.r)

	conn := newConnApi(m.s)
	conn.route(m.r)

	// 管理者api
	manager := newManager(m.s)
	manager.route(m.r)

	// 压测api
	if options.G.Stress {
		stress := newStress(m.s)
		stress.route(m.r)
	}

	// 标签api
	tag := newTag(m.s)
	tag.route(m.r)

	// // 系统api
	// system := NewSystemAPI(s.s)
	// system.Route(s.r)

	// 分布式api
	clusterServer, ok := service.Cluster.(*cluster.Server)
	if ok {
		clusterServer.ServerAPI(m.r, "/cluster")
	}
	// 监控
	trace.GlobalTrace.Route(m.r)

}

func (m *managerServer) jwtAndTokenAuthMiddleware() wkhttp.HandlerFunc {
	return func(c *wkhttp.Context) {

		fpath := c.Request.URL.Path
		if strings.HasPrefix(fpath, "/manager/login") { // 登录不需要认证
			c.Next()
			return
		}
		if strings.HasPrefix(fpath, "/web") {
			c.Next()
			return
		}
		if strings.HasPrefix(fpath, "/metrics") {
			c.Next()
			return
		}

		// 管理token认证
		token := c.GetHeader("token")
		if strings.TrimSpace(token) != "" && token == options.G.ManagerToken {
			c.Set("username", options.G.ManagerUID)
			c.Next()
			return
		}

		// 认证jwt
		authorization := c.GetHeader("Authorization")
		if authorization == "" {
			authorization = c.Query("Authorization")
		}

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
			return []byte(options.G.Jwt.Secret), nil
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

func parseAddr(addr string) (string, int64) {
	addrPairs := strings.Split(addr, ":")
	if len(addrPairs) < 2 {
		return "", 0
	}
	portInt64, _ := strconv.ParseInt(addrPairs[len(addrPairs)-1], 10, 64)
	return addrPairs[0], portInt64
}
