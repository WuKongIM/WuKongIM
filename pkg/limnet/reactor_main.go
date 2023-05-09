package limnet

import (
	"github.com/WuKongIM/WuKongIM/pkg/limlog"
)

type ReactorMain struct {
	acceptor *Acceptor
	eg       *Engine
	limlog.Log
}

func NewReactorMain(eg *Engine) *ReactorMain {

	return &ReactorMain{
		acceptor: NewAcceptor(eg),
		eg:       eg,
		Log:      limlog.NewLIMLog("ReactorMain"),
	}
}

func (m *ReactorMain) Start() error {
	return m.acceptor.Start()
}

func (m *ReactorMain) Stop() error {
	return m.acceptor.Stop()
}
