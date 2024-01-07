package replica

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type RawReplica struct {
	opts   *Options
	nodeID uint64
	wklog.Log

	// 是否是领导者
	leaderID atomic.Uint64

	committedIndex atomic.Uint64 // 已提交下标

	lastSyncLogLock sync.RWMutex
	lastSyncInfoMap map[uint64]*SyncInfo // 副本日志信息
	// lastSyncLogTimeMap  map[uint64]time.Time   // 副本最后一次来同步日志的时间

	proposeLock     sync.Mutex
	commitLock      sync.Mutex
	requestSyncLock sync.Mutex

	lastLogIndex atomic.Uint64 // 最后一条日志下标
}

func NewRawReplica(nodeID uint64, shardNo string, optList ...Option) *RawReplica {

	opts := NewOptions()
	for _, opt := range optList {
		opt(opts)
	}
	opts.NodeID = nodeID
	opts.ShardNo = shardNo

	r := &RawReplica{
		opts:            opts,
		nodeID:          nodeID,
		Log:             wklog.NewWKLog(fmt.Sprintf("Replica[%d:%s]", nodeID, opts.ShardNo)),
		lastSyncInfoMap: map[uint64]*SyncInfo{},
		// lastSyncLogTimeMap:  make(map[uint64]time.Time),
	}
	r.committedIndex.Store(opts.AppliedIndex)

	var lastLogIndex uint64
	lastLogIndex, err := r.opts.Storage.LastIndex()
	if err != nil {
		r.Panic("get last log index failed", zap.Error(err))
	}
	r.lastLogIndex.Store(lastLogIndex)

	if len(opts.LastSyncInfoMap) > 0 {
		for nodeID, rinfo := range opts.LastSyncInfoMap {
			r.lastSyncInfoMap[nodeID] = &SyncInfo{
				NodeID:       rinfo.NodeID,
				LastLogIndex: rinfo.LastLogIndex,
				LastSyncTime: rinfo.LastSyncTime,
			}
		}
	}
	return r
}

func (r *RawReplica) IsLeader() bool {
	return r.opts.NodeID == r.leaderID.Load()
}

func (r *RawReplica) LeaderID() uint64 {
	return r.leaderID.Load()
}

func (r *RawReplica) ProposeOnlyLocal(data []byte) error {
	r.proposeLock.Lock()
	defer r.proposeLock.Unlock()
	lastIdx, err := r.LastIndex()
	if err != nil {
		return err
	}

	lg := Log{
		Index: lastIdx + 1,
		Data:  data,
	}
	err = r.AppendLogOnlyLocal(lg)
	if err != nil {
		return err
	}
	return nil
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
	return r.opts.Storage.LastIndex()
}

func (r *RawReplica) GetLogs(startLogIndex uint64, limit uint32) ([]Log, error) {
	return r.opts.Storage.GetLogs(startLogIndex, limit)
}

func (r *RawReplica) SyncLogs(nodeID uint64, startLogIndex uint64, limit uint32) ([]Log, error) {
	logs, err := r.GetLogs(startLogIndex, limit)
	if err != nil {
		return nil, err
	}
	r.lastSyncLogLock.Lock()
	syncInfo := r.lastSyncInfoMap[nodeID]
	if syncInfo == nil {
		syncInfo = &SyncInfo{}
		r.lastSyncInfoMap[nodeID] = syncInfo
	}
	syncInfo.LastLogIndex = startLogIndex
	syncInfo.LastSyncTime = uint64(time.Now().Unix())
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

// 检查并提交日志
func (r *RawReplica) CheckAndCommitLogs() error {

	r.commitLock.Lock()
	defer r.commitLock.Unlock()

	lastLogIndex := r.lastLogIndex.Load()
	if r.committedIndex.Load() >= lastLogIndex {
		return nil
	}
	r.Debug("check and commit logs", zap.Uint64("committedIndex", r.committedIndex.Load()), zap.Uint64("lastLogIndex", lastLogIndex))
	logs, err := r.GetLogs(r.committedIndex.Load()+1, r.opts.CommitLimit)
	if err != nil {
		return err
	}
	if len(logs) == 0 {
		return nil
	}
	applied, err := r.opts.OnApply(logs)
	if err != nil {
		r.Panic("apply log failed", zap.Error(err))
		return err
	}
	if applied > r.committedIndex.Load() {
		r.committedIndex.Store(applied)
		err = r.opts.Storage.SetAppliedIndex(applied)
		if err != nil {
			r.Panic("set applied index failed", zap.Error(err))
			return err
		}
	}

	return nil
}

func (r *RawReplica) appendLog(lg Log) error {
	err := r.opts.Storage.AppendLog(lg)
	if err != nil {
		return err
	}
	r.lastLogIndex.Store(lg.Index)
	return nil
}

// 按需要触发同步请求，返回需要通知的副本
func (r *RawReplica) TriggerSendNotifySyncIfNeed() []uint64 {
	needNotifies := make([]uint64, 0, len(r.opts.Replicas))
	lastLogIndex := r.lastLogIndex.Load()

	if lastLogIndex == 0 { // 领导本身没有日志，不需要通知
		return needNotifies
	}

	for _, replicaNodeID := range r.opts.Replicas {
		if replicaNodeID == r.opts.NodeID {
			continue
		}
		r.lastSyncLogLock.Lock()
		syncInfo := r.lastSyncInfoMap[replicaNodeID]
		r.lastSyncLogLock.Unlock()
		if syncInfo == nil || lastLogIndex >= syncInfo.LastLogIndex {
			needNotifies = append(needNotifies, replicaNodeID)
		}
	}
	if len(needNotifies) > 0 {
		_, _ = r.SendNotifySync(needNotifies)
	}
	return needNotifies
}

func (r *RawReplica) SendNotifySyncToAll() (bool, error) {

	return r.SendNotifySync(r.opts.Replicas)
}

func (r *RawReplica) SendNotifySync(replicas []uint64) (bool, error) {
	existErr := false
	for _, replicaNodeID := range replicas {
		if r.opts.NodeID == replicaNodeID {
			continue
		}
		err := r.opts.Transport.SendSyncNotify(replicaNodeID, &SyncNotify{
			ShardNo:  r.opts.ShardNo,
			LeaderID: r.LeaderID(),
		})
		if err != nil {
			existErr = true
			r.Warn("send syncNotify failed", zap.Error(err), zap.Uint64("replicaNodeID", replicaNodeID), zap.String("shardNo", r.opts.ShardNo))
		}
	}
	return existErr, nil
}

// 同步领导节点日志并根据需要通知领导已同步至最新
func (r *RawReplica) RequestSyncLogsAndNotifyLeaderIfNeed() (int, error) {
	return r.RequestSyncLogsAndNotifyLeaderIfNeedWithLeaderID(r.LeaderID())
}

// 同步领导节点日志并根据需要通知领导已同步至最新
func (r *RawReplica) RequestSyncLogsAndNotifyLeaderIfNeedWithLeaderID(leaderID uint64) (int, error) {
	count, err := r.RequestSyncLogsWithLeaderID(leaderID)
	if err != nil { // 如果同步报错这里忽略掉，因为领导节点还会继续延迟发送同步通知
		return 0, err
	}
	// 如果同步到的日志大于0，并小于同步limit的限制，则立马发起再次同步（最后一次同步的主要目的是告诉领导节点我已经达到最新了）
	// 如果同步到的数量大于等于limit的数量，说明本节点落后日志太多，这里不需要立马去同步了，因为无所谓了，等待主节点的同步通知即可（如果落后太多还一直去同步的话如果这种节点很多的话会导致占用大量带宽和cpu）
	// 落后太多就动态领导节点主动发起同步通知，再去同步
	if count > 0 && count < int(r.opts.SyncLimit) {
		count, err = r.RequestSyncLogsWithLeaderID(leaderID) // 这次请求的主要目的，告诉领导我已经同步完最新日志
		if err != nil {
			return 0, err
		}
	}
	return count, nil
}

// 请求同步日志
func (r *RawReplica) RequestSyncLogs() (int, error) {
	return r.RequestSyncLogsWithLeaderID(r.LeaderID())
}

// 请求同步日志
func (r *RawReplica) RequestSyncLogsWithLeaderID(leaderID uint64) (int, error) {

	r.requestSyncLock.Lock()
	defer r.requestSyncLock.Unlock()

	lastIdx, err := r.LastIndex()
	if err != nil {
		r.Error("get last index failed", zap.Error(err))
		return 0, err
	}
	if leaderID == 0 {
		return 0, errors.New("leaderID is 0")
	}
	if leaderID == r.opts.NodeID {
		return 0, errors.New("leaderID is self")
	}
	resp, err := r.opts.Transport.SyncLog(leaderID, &SyncReq{
		ShardNo:       r.opts.ShardNo,
		Limit:         r.opts.SyncLimit,
		StartLogIndex: lastIdx + 1,
	})
	if err != nil {
		r.Error("sync log failed", zap.Error(err))
		return 0, err
	}
	r.Debug("RequestSyncLogsWithLeaderID......", zap.Uint64("StartLogIndex", lastIdx+1), zap.Uint64("leaderID", leaderID))

	for _, lg := range resp.Logs {
		r.Debug("append log to storage", zap.Uint64("logIndex", lg.Index))
		err = r.appendLog(lg)
		if err != nil {
			r.Panic("append log failed", zap.Error(err))
		}
	}
	// 检查和提交日志
	err = r.CheckAndCommitLogs()
	if err != nil {
		r.Panic("CheckAndCommitLogs failed", zap.Error(err))
	}

	return len(resp.Logs), nil
}
