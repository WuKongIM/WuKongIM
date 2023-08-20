package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/transporter"
)

type Storage struct {
	wkstore.StoreReader
	wkstore.StoreWriter
	s           *Server
	fileStorage *wkstore.FileStore
	wklog.Log
	doCommand func(cmd *transporter.CMDReq) (*transporter.CMDResp, error)
}

func NewStorage(cfg *wkstore.StoreConfig, s *Server, doCommand func(cmd *transporter.CMDReq) (*transporter.CMDResp, error)) *Storage {
	st := &Storage{
		Log:       wklog.NewWKLog("storage"),
		doCommand: doCommand,
		s:         s,
	}
	st.fileStorage = wkstore.NewFileStore(cfg)
	st.StoreReader = st.fileStorage
	return st
}

func (s *Storage) Close() error {
	return s.fileStorage.Close()
}
func (s *Storage) Open() error {
	return s.fileStorage.Open()
}

func (s *Storage) UpdateUserToken(uid string, deviceFlag uint8, deviceLevel uint8, token string) error {
	req := transporter.NewCMDReq(s.s.reqIDGen.Next(), CMDUpdateUserToken.Uint32())
	data := EncodeUserToken(uid, deviceFlag, deviceLevel, token)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}
