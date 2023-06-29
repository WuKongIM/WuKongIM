package server

import (
	"net/http"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
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
	r.Use(wkhttp.CORSMiddleware())

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

	s.r.Use(func(c *wkhttp.Context) { // 管理者权限判断
		if strings.TrimSpace(s.s.opts.ManagerToken) == "" {
			c.Next()
			return
		}
		managerToken := c.GetHeader("token")
		if managerToken != s.s.opts.ManagerToken {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})

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
}

func (s *APIServer) setRoutes() {
	connz := NewConnzAPI(s.s)
	connz.Route(s.r)

	varz := NewVarzAPI(s.s)
	varz.Route(s.r)

	// 用户相关API
	u := NewUserAPI(s.s)
	u.Route(s.r)

	// 频道相关API
	channel := NewChannelAPI(s.s)
	channel.Route(s.r)

	// 最近会话API
	conversation := NewConversationAPI(s.s)
	conversation.Route(s.r)

	// 消息相关API
	message := NewMessageAPI(s.s)
	message.Route(s.r)

	// 路由api
	routeapi := NewRouteAPI(s.s)
	routeapi.Route(s.r)
}
