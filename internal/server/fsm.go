package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/transporter"
)

type FSM struct {
	store wkstore.Store
}

func NewFSM(store wkstore.Store) *FSM {

	return &FSM{
		store: store,
	}
}

func (f *FSM) Apply(req *transporter.CMDReq) (*transporter.CMDResp, error) {
	switch CMDType(req.Type) {
	case CMDUpdateUserToken:
		return f.applyUpdateUserToken((*CMDReq)(req))
	}
	return nil, nil
}

func (f *FSM) applyUpdateUserToken(req *CMDReq) (*transporter.CMDResp, error) {

	uid, deviceFlag, deviceLevel, token, err := req.DecodeUserToken()
	if err != nil {
		return nil, err
	}
	if err = f.store.UpdateUserToken(uid, deviceFlag, deviceLevel, token); err != nil {
		return nil, err
	}
	return nil, nil
}
