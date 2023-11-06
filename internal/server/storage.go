package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
)

type Storage struct {
	wkstore.StoreReader
	wkstore.StoreWriter
	s           *Server
	fileStorage *wkstore.FileStore
	wklog.Log
}

func NewStorage(cfg *wkstore.StoreConfig, s *Server, doCommand func(cmd *CMDReq) (*CMDResp, error)) *Storage {
	st := &Storage{
		Log: wklog.NewWKLog("storage"),
		s:   s,
	}
	st.fileStorage = wkstore.NewFileStore(cfg)
	st.StoreReader = st.fileStorage
	st.StoreWriter = NewStorageWriter(s, doCommand)
	return st
}

func (s *Storage) Close() error {
	return s.fileStorage.Close()
}
func (s *Storage) Open() error {
	return s.fileStorage.Open()
}
