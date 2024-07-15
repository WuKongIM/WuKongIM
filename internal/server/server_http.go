package server

import (
	"net/http"
	"strings"

	cluster "github.com/WuKongIM/WuKongIM/pkg/cluster/clusterserver"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

// APIServer ApiServer
type APIServer struct {
	r    *wkhttp.WKHttp
	addr string
	s    *Server
	wklog.Log
}

// NewAPIServer new一个api server
func NewAPIServer(s *Server) *APIServer {
	r := wkhttp.New()

	if s.opts.PprofOn {
		pprof.Register(r.GetGinRoute()) // 注册pprof
	}

	hs := &APIServer{
		r:    r,
		addr: s.opts.HTTPAddr,
		s:    s,
		Log:  wklog.NewWKLog("APIServer"),
	}
	return hs
}

// Start 开始
func (s *APIServer) Start() {

	// 跨域
	s.r.Use(wkhttp.CORSMiddleware())
	// 带宽流量计算中间件
	s.r.Use(bandwidthMiddleware())
	// jwt和token认证中间件
	s.r.Use(s.jwtAndTokenAuthMiddleware())

	s.setRoutes()
	go func() {
		err := s.r.Run(s.addr) // listen and serve
		if err != nil {
			panic(err)
		}
	}()
	s.Info("Server started", zap.String("addr", s.addr))
}

// Stop 停止服务
func (s *APIServer) Stop() {
	s.Debug("stop...")
}

func (s *APIServer) setRoutes() {
	connz := NewConnzAPI(s.s)
	connz.Route(s.r)

	// varz := NewVarzAPI(s.s)
	// varz.Route(s.r)

	// 用户相关API
	u := NewUserAPI(s.s)
	u.Route(s.r)

	// 频道相关API
	channel := NewChannelAPI(s.s)
	channel.Route(s.r)

	// 最近会话API
	conversation := NewConversationAPI(s.s)
	conversation.Route(s.r)

	// // 消息相关API
	message := NewMessageAPI(s.s)
	message.Route(s.r)

	// 路由api
	routeapi := NewRouteAPI(s.s)
	routeapi.Route(s.r)

	// 管理者api
	manager := NewManagerAPI(s.s)
	manager.Route(s.r)

	// // 系统api
	// system := NewSystemAPI(s.s)
	// system.Route(s.r)

	// 分布式api
	clusterServer, ok := s.s.cluster.(*cluster.Server)
	if ok {
		clusterServer.ServerAPI(s.r, "/cluster")
	}
	// 监控
	s.s.trace.Route(s.r)

}

func bandwidthMiddleware() wkhttp.HandlerFunc {

	return func(c *wkhttp.Context) {

		// fpath := c.FullPath()
		// if strings.HasPrefix(fpath, "/metrics") { // 监控不计算外网带宽
		// 	c.Next()
		// 	return
		// }
		// 获取请求大小
		requestSize := computeRequestSize(c.Request)
		trace.GlobalTrace.Metrics.System().ExtranetIncomingAdd(int64(requestSize))

		// 获取响应大小
		blw := &bodyLogWriter{ResponseWriter: c.Writer}
		c.Writer = blw
		c.Next()
		trace.GlobalTrace.Metrics.System().ExtranetOutgoingAdd(int64(blw.size))

	}
}

func (s *APIServer) jwtAndTokenAuthMiddleware() wkhttp.HandlerFunc {
	return func(c *wkhttp.Context) {

		fpath := c.FullPath()
		if strings.HasPrefix(fpath, "/manager/login") { // 登录不需要认证
			c.Next()
			return
		}

		// 管理token认证
		token := c.GetHeader("token")
		if strings.TrimSpace(token) != "" && token == s.s.opts.ManagerToken {
			c.Set("username", s.s.opts.ManagerUID)
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
			return []byte(s.s.opts.Jwt.Secret), nil
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

type bodyLogWriter struct {
	gin.ResponseWriter
	size int
}

func (blw *bodyLogWriter) Write(b []byte) (int, error) {
	blw.size += len(b)
	return blw.ResponseWriter.Write(b)
}

func computeRequestSize(r *http.Request) int {
	// 计算请求头部大小
	requestSize := 0
	if r.URL != nil {
		requestSize += len(r.URL.String())
	}
	requestSize += len(r.Method)
	requestSize += len(r.Proto)
	for name, values := range r.Header {
		requestSize += len(name)
		for _, value := range values {
			requestSize += len(value)
		}
	}
	// 计算请求体大小
	requestSize += int(r.ContentLength)
	return requestSize
}
