package gossip

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/hashicorp/memberlist"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type Server struct {
	gossipServer *memberlist.Memberlist
	wklog.Log
	opts    *Options
	stopper *syncutil.Stopper
}

func NewServer(nodeID uint64, listenAddr string, opts ...Option) *Server {
	lg := wklog.NewWKLog(fmt.Sprintf("gossip.Server[%d]", nodeID))

	defaultOpts := NewOptions()
	defaultOpts.NodeID = nodeID
	defaultOpts.ListenAddr = listenAddr
	for _, opt := range opts {
		opt(defaultOpts)
	}

	s := &Server{
		Log:     lg,
		opts:    defaultOpts,
		stopper: syncutil.NewStopper(),
	}

	s.gossipServer = createMemberlist(s) // 创建gossip服务

	return s
}

func (s *Server) Start() error {

	// 加入存在的节点
	s.stopper.RunWorker(func() {
		s.loopJoin()
	})

	return nil
}

func (s *Server) Stop() {
	s.stopper.Stop()
	err := s.gossipServer.Shutdown()
	if err != nil {
		s.Warn("Shutdown gossip server failed!", zap.Error(err))
	}
}

func (s *Server) WaitJoin(timeout time.Duration) error {
	if len(s.opts.Seed) == 0 {
		return nil
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	tick := time.NewTicker(time.Millisecond * 200)

	for {
		select {
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		case <-tick.C:
			if s.getNotJoinNum() == 0 {
				return nil
			}
		case <-s.stopper.ShouldStop():
			return errors.New("server stopped")
		}
	}

}

func (s *Server) MustWaitJoin(timeout time.Duration) {
	err := s.WaitJoin(timeout)
	if err != nil {
		s.Panic("WaitJoin failed!", zap.Error(err))
	}
}

func (s *Server) loopJoin() {

	needJoins := s.getNeedJoin()
	if len(needJoins) > 0 {
		s.join(needJoins)
	}

	tick := time.NewTicker(time.Second * 5)
	for {
		select {
		case <-tick.C:
			needJoins := s.getNeedJoin()
			if len(needJoins) > 0 {
				s.join(needJoins)
			} else {
				return
			}
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *Server) join(nodes []string) {
	if len(nodes) > 0 {
		_, err := s.gossipServer.Join(nodes)
		if err != nil {
			s.Error("Join gossip server failed!", zap.Error(err))
		}
	}
}

func createMemberlist(s *Server) *memberlist.Memberlist {
	memberlistCfg := memberlist.DefaultLocalConfig()
	memberlistCfg.Delegate = s
	memberlistCfg.Events = s
	memberlistCfg.Name = strconv.FormatUint(s.opts.NodeID, 10)
	// memberlistCfg.RequireNodeNames = true
	memberlistCfg.BindAddr, memberlistCfg.BindPort = splitAddr(s.opts.ListenAddr)
	if s.opts.AdvertiseAddr != "" {
		memberlistCfg.AdvertiseAddr, memberlistCfg.AdvertisePort = splitAddr(s.opts.AdvertiseAddr)
	} else {
		memberlistCfg.AdvertisePort = memberlistCfg.BindPort
	}
	gossipServer, err := memberlist.Create(memberlistCfg)
	if err != nil {
		s.Panic("Create gossip server failed!", zap.Error(err))
		return nil
	}
	return gossipServer
}

func (s *Server) getNeedJoin() []string {
	needJoins := make([]string, 0)
	if len(s.opts.Seed) > 0 {
		for _, seed := range s.opts.Seed {
			joined := false
			for _, member := range s.gossipServer.Members() {
				if strings.TrimSpace(seed) != "" && member.Address() == seed {
					joined = true
					break
				}
			}
			if !joined {
				needJoins = append(needJoins, seed)
			}
		}
	}
	return needJoins
}

// 未加入节点数量
func (s *Server) getNotJoinNum() int {
	needJoins := s.getNeedJoin()
	return len(needJoins)
}

// split listenAddr to ip and port
func splitAddr(listenAddr string) (string, int) {
	addrs := strings.Split(listenAddr, ":")
	if len(addrs) == 2 {
		port, _ := strconv.Atoi(addrs[1])
		return addrs[0], port
	}
	return "", 0
}
