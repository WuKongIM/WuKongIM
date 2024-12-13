package server

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type processUser struct {
	s *Server
	wklog.Log
}

func newProcessUser(s *Server) *processUser {

	return &processUser{
		s:   s,
		Log: wklog.NewWKLog("processUser"),
	}
}

func (p *processUser) send(actions []reactor.UserAction) {

	for _, a := range actions {
		switch a.Type {
		case reactor.UserActionElection:
			p.processElection(a)
		case reactor.UserActionAuth:
			p.processAuth(a)
		default:
			fmt.Println("a-->", a.Type.String())

		}
	}
}

// 处理选举
func (p *processUser) processElection(a reactor.UserAction) {
	slotId := p.s.cluster.GetSlotId(a.Uid)
	leaderInfo, err := p.s.cluster.SlotLeaderNodeInfo(slotId)
	if err != nil {
		p.Error("get slot leader info failed", zap.Error(err), zap.Uint32("slotId", slotId))
		return
	}
	if leaderInfo == nil {
		p.Error("slot not exist", zap.Uint32("slotId", slotId))
		return
	}
	if leaderInfo.Id == 0 {
		p.Error("slot leader id is 0", zap.Uint32("slotId", slotId))
		return
	}

	reactor.User.UpdateConfig(a.Uid, reactor.UserConfig{
		LeaderId: leaderInfo.Id,
	})
}

func (p *processUser) processAuth(a reactor.UserAction) {
	fmt.Println("processAuth....")
}
