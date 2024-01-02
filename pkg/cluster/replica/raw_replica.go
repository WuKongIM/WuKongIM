package replica

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/wal"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type RawReplica struct {
	opts   *Options
	nodeID uint64
	walLog *wal.Log
	wklog.Log

	replicaLastLogMap map[uint64]uint64 // 记录每个副本最新的日志下标
	// 是否是领导者
	leaderID atomic.Uint64

	lastSyncLogLock     sync.RWMutex
	lastSyncLogIndexMap map[uint64]uint64    // 副本最后一次来同步日志的下标
	lastSyncLogTimeMap  map[uint64]time.Time // 副本最后一次来同步日志的时间
}

func NewRawReplica(nodeID uint64, shardNo string, optList ...Option) *RawReplica {

	opts := NewOptions()
	for _, opt := range optList {
		opt(opts)
	}
	opts.NodeID = nodeID
	opts.ShardNo = shardNo

	r := &RawReplica{
		opts:                opts,
		nodeID:              nodeID,
		Log:                 wklog.NewWKLog(fmt.Sprintf("Replica[%d:%s]", nodeID, opts.ShardNo)),
		replicaLastLogMap:   make(map[uint64]uint64),
		lastSyncLogIndexMap: map[uint64]uint64{},
		lastSyncLogTimeMap:  make(map[uint64]time.Time),
	}

	return r
}

func (r *RawReplica) Open() error {
	var err error
	r.walLog, err = wal.Open(r.opts.DataDir, wal.DefaultOptions)
	return err
}

func (r *RawReplica) Close() {
	err := r.walLog.Sync()
	if err != nil {
		r.Warn("wal log sync failed", zap.Error(err))
	}
	err = r.walLog.Close()
	if err != nil {
		r.Warn("wal log close failed", zap.Error(err))
	}
}

func (r *RawReplica) IsLeader() bool {
	return r.opts.NodeID == r.leaderID.Load()
}

func (r *RawReplica) AppendLogOnlyLocal(lg Log) error {
	if !r.IsLeader() {
		return errors.New("not leader")
	}
	// 本地追加
	err := r.appendLog(lg)
	if err != nil {
		return err
	}
	return nil
}

func (r *RawReplica) LastIndex() (uint64, error) {
	return r.walLog.LastIndex()
}

func (r *RawReplica) GetLogs(startLogIndex uint64, limit uint32) ([]Log, error) {
	lastIdx, _ := r.LastIndex()
	if startLogIndex > lastIdx {
		return nil, nil
	}

	logs := make([]Log, 0, limit)
	for i := startLogIndex; i <= lastIdx; i++ {
		lg, err := r.readLog(i)
		if err != nil {
			return nil, err
		}
		logs = append(logs, lg)
		if len(logs) > int(limit) {
			break
		}
	}
	return logs, nil
}

func (r *RawReplica) SyncLogs(nodeID uint64, startLogIndex uint64, limit uint32) ([]Log, error) {
	logs, err := r.GetLogs(startLogIndex, limit)
	if err != nil {
		return nil, err
	}
	r.lastSyncLogLock.Lock()
	r.lastSyncLogIndexMap[nodeID] = startLogIndex
	r.lastSyncLogTimeMap[nodeID] = time.Now()
	r.lastSyncLogLock.Unlock()
	return logs, nil
}

func (r *RawReplica) GetOptions() *Options {
	return r.opts
}

func (r *RawReplica) SetLeaderID(id uint64) {
	r.leaderID.Store(id)
}

func (r *RawReplica) SetReplicas(replicas []uint64) {
	r.opts.Replicas = replicas
}

func (r *RawReplica) appendLog(lg Log) error {
	lastIdx, _ := r.LastIndex()
	if lg.Index <= lastIdx {
		return nil
	}
	return r.walLog.Write(lg.Index, lg.Data)
}

func (r *RawReplica) readLog(index uint64) (Log, error) {
	data, err := r.walLog.Read(index)
	if err != nil {
		return Log{}, err
	}
	return Log{
		Index: index,
		Data:  data,
	}, err
}

// 按需要触发同步请求
func (r *RawReplica) TriggerSyncIfNeed() {
	needNotifies := make([]uint64, 0, len(r.opts.Replicas))
	lastLogIndex, err := r.LastIndex()
	if err != nil {
		r.Warn("triggerSyncIfNeed get last log index failed", zap.Error(err))
		return
	}

	r.lastSyncLogLock.Lock()
	for _, replicaNodeID := range r.opts.Replicas {
		if replicaNodeID == r.opts.NodeID {
			continue
		}
		logIndex := r.lastSyncLogIndexMap[replicaNodeID]
		if lastLogIndex >= logIndex {
			needNotifies = append(needNotifies, replicaNodeID)
		}
	}
	if len(needNotifies) > 0 {
		_, _ = r.NotifySync(needNotifies)
	}

}

func (r *RawReplica) NotifySync(replicas []uint64) (bool, error) {
	lastLogIndex, err := r.LastIndex()
	existErr := false
	if err != nil {
		r.Error("get last log index failed", zap.Error(err))
		return true, err
	} else {
		for _, replicaNodeID := range replicas {
			if r.opts.NodeID == replicaNodeID {
				continue
			}
			err = r.opts.Transport.SendSyncNotify(replicaNodeID, r.opts.ShardNo, &SyncNotify{
				ShardNo:  r.opts.ShardNo,
				LogIndex: lastLogIndex,
			})
			if err != nil {
				existErr = true
				r.Warn("send syncNotify failed", zap.Error(err), zap.Uint64("replicaNodeID", replicaNodeID), zap.String("shardNo", r.opts.ShardNo))
			}
		}
	}

	return existErr, nil
}

func (r *RawReplica) HandleSyncNotify(req *SyncNotify) {
	lastIdx, err := r.LastIndex()
	if err != nil {
		r.Error("get last index failed", zap.Error(err))
		return
	}
	resp, err := r.opts.Transport.SyncLog(r.leaderID.Load(), &SyncReq{
		ShardNo:       r.opts.ShardNo,
		Limit:         r.opts.SyncLimit,
		StartLogIndex: lastIdx + 1,
	})
	if err != nil {
		r.Error("sync log failed", zap.Error(err))
		return
	}
	for _, lg := range resp.Logs {
		err = r.appendLog(lg)
		if err != nil {
			r.Error("append log failed", zap.Error(err))
		}
	}
}
