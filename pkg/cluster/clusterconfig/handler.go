package clusterconfig

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

var _ reactor.IHandler = &handler{}

type handler struct {
	rc   *replica.Replica
	cfg  *Config
	opts *Options
	wklog.Log
	leaderId uint64
	storage  *PebbleShardLogStorage
	mu       sync.Mutex
}

func newHandler(cfg *Config, storage *PebbleShardLogStorage, opts *Options) *handler {

	h := &handler{
		cfg:     cfg,
		opts:    opts,
		Log:     wklog.NewWKLog("clusterconfig.handler"),
		storage: storage,
	}
	replicas := make([]uint64, 0, len(opts.InitNodes))
	for replicaId := range opts.InitNodes {
		replicas = append(replicas, replicaId)
	}

	// data, err := cfg.data()
	// if err != nil {
	// 	h.Panic("get config data error", zap.Error(err))
	// }

	// if cfg.version() > 0 {
	// 	err = h.memoryStorage.SetLeaderTermStartIndex(cfg.term(), cfg.version())
	// 	if err != nil {
	// 		h.Panic("set leader term start index error", zap.Error(err))
	// 	}
	// 	_ = h.memoryStorage.AppendLog([]replica.Log{
	// 		{
	// 			Index: cfg.version(),
	// 			Term:  cfg.term(),
	// 			Data:  data,
	// 		},
	// 	})
	// }

	h.rc = replica.New(opts.NodeId, replica.WithLogPrefix("config"), replica.WithReplicas(replicas), replica.WithElectionOn(true), replica.WithStorage(h.storage), replica.WithAppliedIndex(cfg.version()))
	return h
}

// -------------------- implement IHandler --------------------

// LastLogIndexAndTerm 获取最后一条日志的索引和任期
func (h *handler) LastLogIndexAndTerm() (uint64, uint32) {
	return h.rc.LastLogIndex(), h.rc.Term()
}

func (h *handler) HasReady() bool {
	return h.rc.HasReady()
}

// Ready 获取ready事件
func (h *handler) Ready() replica.Ready {
	return h.rc.Ready()
}

// GetAndMergeLogs 获取并合并日志
func (h *handler) GetAndMergeLogs(lastIndex uint64, msg replica.Message) ([]replica.Log, error) {

	unstableLogs := msg.Logs
	startIndex := msg.Index
	if len(unstableLogs) > 0 {
		startIndex = unstableLogs[len(unstableLogs)-1].Index + 1
	}

	var err error
	if lastIndex == 0 {
		lastIndex, err = h.storage.LastIndex()
		if err != nil {
			h.Error("getAndMergeLogs: get last index error", zap.Error(err))
			return nil, err
		}
	}
	var resultLogs []replica.Log
	if startIndex <= lastIndex {
		logs, err := h.getLogs(startIndex, lastIndex+1)
		if err != nil {
			h.Error("get logs error", zap.Error(err), zap.Uint64("startIndex", startIndex), zap.Uint64("lastIndex", lastIndex))
			return nil, err
		}
		resultLogs = extend(unstableLogs, logs)
	}

	return resultLogs, nil
}

func (h *handler) getLogs(startLogIndex uint64, endLogIndex uint64) ([]replica.Log, error) {
	logs, err := h.storage.Logs(startLogIndex, endLogIndex, 0)
	if err != nil {
		h.Error("get logs error", zap.Error(err))
		return nil, err
	}
	return logs, nil
}

// AppendLog 追加日志
func (h *handler) AppendLog(logs []replica.Log) error {
	err := h.storage.AppendLog(logs)
	return err
}

// ApplyLog 应用日志
func (h *handler) ApplyLog(startLogIndex, endLogIndex uint64) error {
	lastLog, err := h.storage.LastLog()
	if err != nil {
		return err
	}
	if replica.IsEmptyLog(lastLog) {
		return nil
	}
	err = h.cfg.apply(lastLog.Data)

	// 触发配置已应用事件
	h.opts.Event.OnAppliedConfig()

	return err
}

// SlowDown 降速
func (h *handler) SlowDown() {
	h.rc.SlowDown()
}

func (h *handler) SpeedLevel() replica.SpeedLevel {
	return h.rc.SpeedLevel()
}

func (h *handler) SetSpeedLevel(level replica.SpeedLevel) {
	h.rc.SetSpeedLevel(level)
}

// SetHardState 设置HardState
func (h *handler) SetHardState(hd replica.HardState) {
	h.mu.Lock()
	defer h.mu.Unlock()
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

func (h *handler) IsLeader() bool {
	return h.isLeader()
}

func (h *handler) isLeader() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.opts.NodeId == h.leaderId
}

func (h *handler) LeaderId() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.leaderId
}

func extend(dst, vals []replica.Log) []replica.Log {
	need := len(dst) + len(vals)
	if need <= cap(dst) {
		return append(dst, vals...) // does not allocate
	}
	buf := make([]replica.Log, need) // allocates precisely what's needed
	copy(buf, dst)
	copy(buf[len(dst):], vals)
	return buf
}
