package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/lni/goutils/syncutil"
)

type storeEvent struct {
	storeMessageChan     chan storeMessageReq
	storeMessageRespChan chan storeMessageResp
	stopper              *syncutil.Stopper
	s                    *Server
	wklog.Log
}

func newStoreEvent(s *Server) *storeEvent {
	return &storeEvent{
		storeMessageChan: make(chan storeMessageReq, 100),
		s:                s,
		stopper:          syncutil.NewStopper(),
		Log:              wklog.NewWKLog("netEvent"),
	}
}

func (s *storeEvent) start() {
	s.stopper.RunWorker(s.loopStoreMessages)
}

func (s *storeEvent) stop() {
	s.stopper.Stop()
}

func (s *storeEvent) loopStoreMessages() {
	reqs := make([]storeMessageReq, 0, 100)
	mergeReqs := make([]storeMessageReq, 0, 100)
	for {
		select {
		case msg := <-s.storeMessageChan:
			reqs = append(reqs, msg)
			s.getAllStoreMessageReq(reqs)
			s.mergeStoreMessageReq(reqs, mergeReqs)

			for _, req := range mergeReqs {
				go s.requestStoreMessages(req) // todo: 这里可以使用一个协程池
			}
			reqs = reqs[:0]
			mergeReqs = mergeReqs[:0]
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *storeEvent) requestStoreMessages(req storeMessageReq) {
	err := s.s.store.AppendMessages(req.channelId, req.channelType, req.messages)
	s.storeMessageRespChan <- storeMessageResp{
		err:         err,
		channelId:   req.channelId,
		channelType: req.channelType,
		messages:    req.messages,
	}
}

func (s *storeEvent) getAllStoreMessageReq(reqs []storeMessageReq) {
	for {
		select {
		case req := <-s.storeMessageChan:
			reqs = append(reqs, req)
		default:
			return
		}
	}
}

// 根据channel合并storeMessageReq
func (s *storeEvent) mergeStoreMessageReq(reqs []storeMessageReq, resultReqs []storeMessageReq) {
	for _, req := range reqs {
		for _, newReq := range resultReqs {
			if req.channelId == newReq.channelId && req.channelType == newReq.channelType {
				newReq.messages = append(newReq.messages, req.messages...)
				goto NEXT
			}
		}
		resultReqs = append(resultReqs, req)
	NEXT:
	}
}

type storeMessageReq struct {
	channelId   string
	channelType uint8
	messages    []wkstore.Message
}

type storeMessageResp struct {
	channelId   string
	channelType uint8
	messages    []wkstore.Message
	err         error
}
