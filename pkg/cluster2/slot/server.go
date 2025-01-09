package slot

import (
	"context"
	"fmt"
	"path"
	"strconv"

	"github.com/WuKongIM/WuKongIM/pkg/raft/raftgroup"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/bwmarrin/snowflake"
	"go.uber.org/zap"
)

type Server struct {
	raftGroup *raftgroup.RaftGroup
	storage   *PebbleShardLogStorage
	opts      *Options
	wklog.Log
	genLogId *snowflake.Node
}

func NewServer(opts *Options) *Server {
	s := &Server{
		opts: opts,
		Log:  wklog.NewWKLog("slot.Server"),
	}
	s.storage = NewPebbleShardLogStorage(s, path.Join(opts.DataDir, "logdb"), uint32(opts.SlotDbShardNum))
	s.raftGroup = raftgroup.New(raftgroup.NewOptions(raftgroup.WithStorage(s.storage), raftgroup.WithTransport(opts.Transport)))
	var err error
	s.genLogId, err = snowflake.NewNode(int64(opts.NodeId))
	if err != nil {
		s.Panic("snowflake.NewNode failed", zap.Error(err))
	}
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

func (s *Server) GenLogId() uint64 {
	return uint64(s.genLogId.Generate().Int64())
}

// 获取频道所在的slotId
func (s *Server) getSlotId(v string) uint32 {
	var slotCount uint32 = s.opts.Node.SlotCount()
	if slotCount == 0 {
		slotCount = s.opts.SlotCount
	}
	return wkutil.GetSlotNum(int(slotCount), v)
}
