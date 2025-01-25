package api

import (
	"net/http"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	cluster "github.com/WuKongIM/WuKongIM/pkg/cluster/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type apiServer struct {
	r    *wkhttp.WKHttp
	addr string
	s    *Server
	wklog.Log
}

// NewAPIServer new一个api server
func newApiServer(s *Server) *apiServer {
	// r := wkhttp.New()
	log := wklog.NewWKLog("apiServer")
	r := wkhttp.NewWithLogger(wkhttp.LoggerWithWklog(log))

	if options.G.PprofOn {
		pprof.Register(r.GetGinRoute()) // 注册pprof
	}

	hs := &apiServer{
		r:    r,
		addr: options.G.HTTPAddr,
		s:    s,
		Log:  log,
	}
	return hs
}

// Start 开始
func (s *apiServer) start() {

	s.r.Use(func(c *wkhttp.Context) { // 管理者权限判断
		if strings.TrimSpace(options.G.ManagerToken) == "" {
			c.Next()
			return
		}
		managerToken := c.GetHeader("token")
		if managerToken != options.G.ManagerToken {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})

	// 跨域
	s.r.Use(wkhttp.CORSMiddleware())
	// 带宽流量计算中间件
	s.r.Use(bandwidthMiddleware())

	s.setRoutes()
	go func() {
		err := s.r.Run(s.addr) // listen and serve
		if err != nil {
			panic(err)
		}
	}()
	s.Info("ApiServer started", zap.String("addr", s.addr))
}

// Stop 停止服务
func (s *apiServer) stop() {
	s.Debug("stop...")
}

func (s *apiServer) setRoutes() {

	s.r.GET("/health", func(c *wkhttp.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	s.r.GET("/migrate/result", func(c *wkhttp.Context) {
		c.JSON(http.StatusOK, s.s.migrateTask.GetMigrateResult())
	})

	// route
	rt := newRoute(s.s)
	rt.route(s.r)
	// connz
	connz := newConnz(s.s)
	connz.route(s.r)
	// varz
	varz := newVarz(s.s)
	varz.route(s.r)
	// user
	user := newUser(s.s)
	user.route(s.r)
	// conn
	conn := newConnApi(s.s)
	conn.route(s.r)
	// channel
	ch := newChannel(s.s)
	ch.route(s.r)
	// message
	msg := newMessage(s.s)
	msg.route(s.r)
	// conversation
	conves := newConversation(s.s)
	conves.route(s.r)
	// manager
	mg := newManager(s.s)
	mg.route(s.r)
	// stress
	if options.G.Stress {
		st := newStress(s.s)
		st.route(s.r)
	}
	// stream
	stream := newStream(s.s)
	stream.route(s.r)

	// tag
	tag := newTag(s.s)
	tag.route(s.r)

	// 分布式api
	clusterServer, ok := service.Cluster.(*cluster.Server)
	if ok {
		clusterServer.ServerAPI(s.r, "/cluster")
	}

	// // 系统api
	// system := NewSystemAPI(s.s)
	// system.Route(s.r)

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
