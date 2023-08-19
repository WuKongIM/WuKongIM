package wraft

type FSM interface {
	Apply(req *CMDReq) (*CMDResp, error)
}
