package slot

import (
	"context"
	"fmt"
	"path"
	"strconv"

	"github.com/WuKongIM/WuKongIM/pkg/raft/raftgroup"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type Server struct {
	raftGroup *raftgroup.RaftGroup
	storage   *PebbleShardLogStorage
	opts      *Options
	wklog.Log
}

func NewServer(opts *Options) *Server {
	s := &Server{
		opts: opts,
		Log:  wklog.NewWKLog("slot.Server"),
	}
	s.raftGroup = raftgroup.New(raftgroup.NewOptions(raftgroup.WithStorage(s.storage), raftgroup.WithTransport(opts.Transport)))
	s.storage = NewPebbleShardLogStorage(path.Join(opts.DataDir, "logdb"), uint32(opts.SlotDbShardNum))
	return s
}

func (s *Server) Start() error {

	err := s.storage.Open()
	if err != nil {
		return err
	}

	fmt.Println("slot server start")

	err = s.raftGroup.Start()
	if err != nil {
		return err
	}
	return nil
}

func (s *Server) Stop() {
	s.raftGroup.Stop()

	err := s.storage.Close()
	if err != nil {
		s.Error("storage close failed", zap.Error(err))
	}
}

func (s *Server) AddEvent(shardNo string, event types.Event) {
	s.raftGroup.AddEvent(shardNo, event)
}

func (s *Server) Propose(raftKey string, id uint64, data []byte) (*types.ProposeResp, error) {
	return s.raftGroup.Propose(raftKey, id, data)
}

func SlotIdToKey(slotId uint32) string {
	return strconv.FormatUint(uint64(slotId), 10)
}

func (s *Server) WaitAllSlotReady(ctx context.Context, slotCount int) error {

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		ready := true
		rafts := s.raftGroup.GetRafts()
		if slotCount == len(rafts) {
			for _, raft := range rafts {
				if raft.LeaderId() == 0 {
					ready = false
					break
				}
			}
		} else {
			ready = false
		}

		if ready {
			return nil
		}
	}
}
