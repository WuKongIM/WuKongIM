package wraft

import "github.com/WuKongIM/WuKongIM/pkg/wraft/transporter"

type FSM interface {
	Apply(req *transporter.CMDReq) (*transporter.CMDResp, error)
}
