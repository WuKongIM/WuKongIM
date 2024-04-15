package clusterconfig

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

var _ reactor.IHandler = &handler{}

type handler struct {
	memoryStorage *replica.MemoryStorage
	rc            *replica.Replica
	cfg           *Config
	opts          *Options
	wklog.Log
	leaderId uint64
}

func newHandler(cfg *Config, opts *Options) *handler {
	h := &handler{
		memoryStorage: replica.NewMemoryStorage(),
		cfg:           cfg,
		opts:          opts,
		Log:           wklog.NewWKLog("clusterconfig.handler"),
	}
	data, err := cfg.data()
	if err != nil {
		h.Panic("get config data error", zap.Error(err))
	}
	err = h.memoryStorage.AppendLog([]replica.Log{
		{
			Id:    cfg.id(),
			Index: cfg.version(),
			Term:  cfg.term(),
			Data:  data,
		},
	})
	if err != nil {
		h.Panic("append log error", zap.Error(err))
	}

	replicas := make([]uint64, 0, len(opts.InitNodes))
	for replicaId := range opts.InitNodes {
		replicas = append(replicas, replicaId)
	}

	h.rc = replica.New(opts.NodeId, replica.WithReplicas(replicas), replica.WithElectionOn(true), replica.WithStorage(h.memoryStorage), replica.WithAppliedIndex(cfg.version()))
	return h
}

// -------------------- implement IHandler --------------------

// LastLogIndexAndTerm 获取最后一条日志的索引和任期
func (h *handler) LastLogIndexAndTerm() (uint64, uint32) {
	lg := h.memoryStorage.LastLog()
	return lg.Index, h.cfg.term()
}

// Ready 获取ready事件
func (h *handler) Ready() replica.Ready {
	return h.rc.Ready()
}

// GetAndMergeLogs 获取并合并日志
func (h *handler) GetAndMergeLogs(msg replica.Message) ([]replica.Log, error) {

	data, err := h.cfg.data()
	if err != nil {
		return nil, err
	}
	return []replica.Log{
		{
			Id:    h.cfg.id(),
			Index: h.cfg.version(),
			Term:  h.cfg.term(),
			Data:  data,
		},
	}, nil
}

func (h *handler) Logs(startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]replica.Log, error) {
	return h.memoryStorage.Logs(startLogIndex, endLogIndex)
}

// AppendLog 追加日志
func (h *handler) AppendLog(logs []replica.Log) error {
	_ = h.memoryStorage.TruncateLogTo(1)
	err := h.memoryStorage.AppendLog(logs)
	return err
}

// ApplyLog 应用日志
func (h *handler) ApplyLog(logs []replica.Log) error {
	if len(logs) == 0 {
		return nil
	}
	lastLog := logs[len(logs)-1]
	err := h.cfg.apply(lastLog.Data)
	return err
}

// SlowDown 降速
func (h *handler) SlowDown() {

}

// SetHardState 设置HardState
func (h *handler) SetHardState(hd replica.HardState) {
	h.leaderId = hd.LeaderId
}

// Tick tick
func (h *handler) Tick() {
	h.rc.Tick()
}

// Step 步进消息
func (h *handler) Step(m replica.Message) error {
	return h.rc.Step(m)
}

// SetLastIndex 设置最后一条日志的索引
func (h *handler) SetLastIndex(index uint64) error {
	return nil
}

// SetAppliedIndex 设置已应用的索引
func (h *handler) SetAppliedIndex(index uint64) error {
	return nil
}

// IsPrepared 是否准备好
func (h *handler) IsPrepared() bool {
	return true
}

func (h *handler) isLeader() bool {
	return h.opts.NodeId == h.leaderId
}
