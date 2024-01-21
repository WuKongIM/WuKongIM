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

type Role uint8

const (
	RoleFollower Role = iota
	RoleCandidate
	RoleLeader
)

type RawReplica struct {
	opts   *Options
	nodeID uint64
	wklog.Log

	role Role

	sync.Mutex

	// 领导ID
	leaderID        atomic.Uint64
	currentTerm     atomic.Uint32 // 副本当前任期
	committedIndex  atomic.Uint64 // 已提交下标
	appliedIndex    atomic.Uint64 // 已应用下标
	lastEndLogIndex atomic.Uint64 // 最后一条日志下标

	commitWait *commitWait // 提交等待

	replicas []uint64 // 副本节点列表 （不包含当前节点）

	lastSyncLogLock sync.RWMutex
	lastSyncInfoMap map[uint64]*SyncInfo // 副本日志信息
	// lastSyncLogTimeMap  map[uint64]time.Time   // 副本最后一次来同步日志的时间

	proposeLock     sync.Mutex
	commitLock      sync.Mutex
	requestSyncLock sync.Mutex
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
		role:            RoleFollower,
		commitWait:      newCommitWait(),
		// lastSyncLogTimeMap:  make(map[uint64]time.Time),
	}
	r.committedIndex.Store(opts.AppliedIndex)
	r.appliedIndex.Store(opts.AppliedIndex)
	r.currentTerm.Store(opts.CurrentTerm)

	for _, v := range opts.Replicas {
		if v == opts.NodeID {
			continue
		}
		r.replicas = append(r.replicas, v)
	}

	var lastLogIndex uint64
	lastLogIndex, err := r.opts.Storage.LastIndex()
	if err != nil {
		r.Panic("get last log index failed", zap.Error(err))
	}
	r.lastEndLogIndex.Store(lastLogIndex)

	if len(opts.LastSyncInfoMap) > 0 {
		for nodeID, rinfo := range opts.LastSyncInfoMap {
			r.lastSyncInfoMap[nodeID] = &SyncInfo{
				NodeID:           rinfo.NodeID,
				LastSyncLogIndex: rinfo.LastSyncLogIndex,
				LastSyncTime:     rinfo.LastSyncTime,
			}
		}
	}
	return r
}

func (r *RawReplica) IsLeader() bool {
	return r.isLeader()
}

func (r *RawReplica) isLeader() bool {
	return r.opts.NodeID == r.leaderID.Load()
}

func (r *RawReplica) LeaderID() uint64 {
	return r.leaderID.Load()
}

func (r *RawReplica) Propose(data []byte) (uint64, error) {
	r.Lock()

	if !r.isLeader() {
		return 0, errors.New("not leader")
	}

	lastIdx, err := r.lastIndex()
	if err != nil {
		r.Unlock()
		return 0, err
	}

	lastEndLogIndex := lastIdx + 1
	lg := Log{
		Index: lastEndLogIndex,
		Term:  r.currentTerm.Load(),
		Data:  data,
	}

	// 本地追加
	err = r.appendLog(lg)
	if err != nil {
		r.Unlock()
		return 0, err
	}

	if len(r.replicas) == 0 { // 如果没有副本则直接提交
		r.committedIndex.Store(lastEndLogIndex)
		r.Unlock()
		return lastEndLogIndex, nil
	}
	r.Unlock()

	// 通知副本去同步并等待大多数副本同步一致，也就是提交
	err = r.notifyAndWaitCommit(lastEndLogIndex)
	if err != nil {
		return 0, err
	}
	return lastEndLogIndex, nil
}

func (r *RawReplica) notifyAndWaitCommit(lastEndLogIndex uint64) error {
	waitC := r.commitWait.addWaitIndex(lastEndLogIndex)
	// 通知副本同步
	r.opts.Transport.SendSyncNotify(r.replicas, &SyncNotify{
		ShardNo:        r.opts.ShardNo,
		LeaderID:       r.opts.NodeID,
		CommittedIndex: r.committedIndex.Load(),
	})

	// 等待提交
	select {
	case <-waitC:
		return nil
	case <-time.After(r.opts.ProposeTimeout):
		return errors.New("propose timeout")
	}
}

func (r *RawReplica) ProposeOnlyLocal(data []byte) (uint64, error) {
	r.proposeLock.Lock()
	defer r.proposeLock.Unlock()
	lastIdx, err := r.LastIndex()
	if err != nil {
		return 0, err
	}

	lg := Log{
		Index: lastIdx + 1,
		Term:  r.currentTerm.Load(),
		Data:  data,
	}
	err = r.AppendLogOnlyLocal(lg)
	if err != nil {
		return 0, err
	}
	return lastIdx + 1, nil
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

func (r *RawReplica) BecomeLeader() {
	r.Lock()
	defer r.Unlock()
	if r.role == RoleLeader {
		return
	}
	r.role = RoleLeader
	r.SetLeaderID(r.opts.NodeID)

}

func (r *RawReplica) BecomeFollower(leaderID uint64) {
	r.Lock()
	defer r.Unlock()
	r.role = RoleFollower
	if r.leaderID.Load() != leaderID {
		r.SetLeaderID(leaderID)
	}
}

func (r *RawReplica) BecomeCandidate() {
	r.Lock()
	defer r.Unlock()
	if r.role == RoleCandidate {
		return
	}
	r.role = RoleCandidate
	r.SetLeaderID(0)
}

func (r *RawReplica) LastIndex() (uint64, error) {
	return r.lastIndex()
}

func (r *RawReplica) lastIndex() (uint64, error) {
	return r.opts.Storage.LastIndex()
}

func (r *RawReplica) GetLogs(startLogIndex uint64, endLogIndex uint64, limit uint32) ([]Log, error) {
	return r.opts.Storage.GetLogs(startLogIndex, endLogIndex, limit)
}

func (r *RawReplica) SyncLogs(nodeID uint64, startLogIndex uint64, limit uint32) ([]Log, error) {
	logs, err := r.GetLogs(startLogIndex, 0, limit)
	if err != nil {
		return nil, err
	}
	r.lastSyncLogLock.Lock()
	syncInfo := r.lastSyncInfoMap[nodeID]
	if syncInfo == nil {
		syncInfo = &SyncInfo{}
		r.lastSyncInfoMap[nodeID] = syncInfo
	}
	syncInfo.LastSyncLogIndex = startLogIndex
	syncInfo.LastSyncTime = uint64(time.Now().UnixNano())
	r.lastSyncLogLock.Unlock()

	newCommitted := r.calcCommittedIndex()
	if newCommitted > r.committedIndex.Load() {
		oldCommitted := r.committedIndex.Load()
		r.committedIndex.Store(newCommitted)
		r.commitWait.commitIndex(newCommitted) // 响应等待者
		if r.opts.OnCommit != nil {
			r.opts.OnCommit(oldCommitted, newCommitted)
		}
	}
	return logs, nil
}

// 通过副本同步信息计算已提交下标
func (r *RawReplica) calcCommittedIndex() uint64 {
	r.lastSyncLogLock.Lock()
	defer r.lastSyncLogLock.Unlock()

	committed := r.committedIndex.Load()
	quorum := len(r.replicas) / 2 // r.replicas 不包含本节点

	if quorum == 0 { // 如果少于或等于一个节点，那么直接返回最后一条日志下标
		return r.lastEndLogIndex.Load()
	}

	// 获取比指定参数小的最大日志下标
	getMaxLogIndexLessThanParam := func(maxIndex uint64) uint64 {
		secondMaxIndex := uint64(0)
		for _, syncInfo := range r.lastSyncInfoMap {
			if syncInfo.LastSyncLogIndex < maxIndex || maxIndex == 0 {
				if secondMaxIndex < syncInfo.LastSyncLogIndex {
					secondMaxIndex = syncInfo.LastSyncLogIndex
				}
			}
		}
		return secondMaxIndex
	}

	maxLogIndex := uint64(0)
	newCommitted := uint64(0)
	for {
		count := 0
		maxLogIndex = getMaxLogIndexLessThanParam(maxLogIndex)
		if maxLogIndex == 0 {
			break
		}
		if maxLogIndex <= committed {
			break
		}
		if maxLogIndex > r.lastEndLogIndex.Load() {
			continue
		}
		for _, syncInfo := range r.lastSyncInfoMap {
			if syncInfo.LastSyncLogIndex >= maxLogIndex {
				count++
			}
			if count >= quorum {
				newCommitted = maxLogIndex
				break
			}
		}
	}
	if newCommitted > committed {
		return newCommitted
	}
	return committed

}

func (r *RawReplica) GetOptions() *Options {
	return r.opts
}

func (r *RawReplica) SetLeaderID(id uint64) {
	r.leaderID.Store(id)
}

func (r *RawReplica) SetReplicas(replicas []uint64) {
	r.Lock()
	defer r.Unlock()
	r.opts.Replicas = replicas
	r.replicas = make([]uint64, 0)
	for _, v := range r.opts.Replicas {
		if v == r.opts.NodeID {
			continue
		}
		r.replicas = append(r.replicas, v)
	}
}

// 检查并提交日志
func (r *RawReplica) CheckAndCommitLogs() error {

	r.commitLock.Lock()
	defer r.commitLock.Unlock()

	lastLogIndex := r.lastEndLogIndex.Load()
	if r.committedIndex.Load() >= lastLogIndex {
		return nil
	}
	if r.committedIndex.Load() == 0 {
		return nil
	}
	if r.appliedIndex.Load() >= r.committedIndex.Load() {
		return nil
	}
	logs, err := r.GetLogs(r.appliedIndex.Load()+1, r.committedIndex.Load()+1, r.opts.CommitLimit)
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
		r.appliedIndex.Store(applied)
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
	r.lastEndLogIndex.Store(lg.Index)
	return nil
}

// 按需要触发同步请求，返回需要通知的副本
func (r *RawReplica) TriggerSendNotifySyncIfNeed() []uint64 {
	needNotifies := make([]uint64, 0, len(r.opts.Replicas))
	lastLogIndex := r.lastEndLogIndex.Load()

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
		if syncInfo == nil || lastLogIndex >= syncInfo.LastSyncLogIndex {
			needNotifies = append(needNotifies, replicaNodeID)
		}
	}
	if len(needNotifies) > 0 {
		r.SendNotifySync(needNotifies)
	}
	return needNotifies
}

func (r *RawReplica) SendNotifySyncToAll() {

	r.SendNotifySync(r.opts.Replicas)
}

func (r *RawReplica) SendNotifySync(replicas []uint64) {
	// startTime := time.Now().UnixMilli()
	// r.Debug("send notify sync", zap.Uint64("leaderID", r.opts.NodeID), zap.String("shardNo", r.opts.ShardNo), zap.Uint64s("replicas", replicas))
	r.opts.Transport.SendSyncNotify(replicas, &SyncNotify{
		ShardNo:        r.opts.ShardNo,
		LeaderID:       r.LeaderID(),
		CommittedIndex: r.committedIndex.Load(),
	})
}

// // 同步领导节点日志并根据需要通知领导已同步至最新
// func (r *RawReplica) RequestSyncLogsAndNotifyLeaderIfNeed() (int, error) {
// 	return r.RequestSyncLogsAndNotifyLeaderIfNeedWithLeaderID(r.LeaderID())
// }

// // 同步领导节点日志并根据需要通知领导已同步至最新
// func (r *RawReplica) RequestSyncLogsAndNotifyLeaderIfNeedWithLeaderID(leaderID uint64) (int, error) {
// 	count, err := r.RequestSyncLogsWithLeaderID(leaderID)
// 	if err != nil { // 如果同步报错这里忽略掉，因为领导节点还会继续延迟发送同步通知
// 		return 0, err
// 	}
// 	// 如果同步到的日志大于0，并小于同步limit的限制，则立马发起再次同步（最后一次同步的主要目的是告诉领导节点我已经达到最新了）
// 	// 如果同步到的数量大于等于limit的数量，说明本节点落后日志太多，这里不需要立马去同步了，因为无所谓了，等待主节点的同步通知即可（如果落后太多还一直去同步的话如果这种节点很多的话会导致占用大量带宽和cpu）
// 	// 落后太多就动态领导节点主动发起同步通知，再去同步
// 	if count > 0 && count < int(r.opts.SyncLimit) {
// 		count, err = r.RequestSyncLogsWithLeaderID(leaderID) // 这次请求的主要目的，告诉领导我已经同步完最新日志
// 		if err != nil {
// 			return 0, err
// 		}
// 	}
// 	return count, nil
// }

// 请求同步日志
// 返回同步的日志数量
func (r *RawReplica) RequestSyncLogs(req *SyncNotify) error {
	r.requestSyncLock.Lock()
	defer r.requestSyncLock.Unlock()

	oldCommittedIndex := r.committedIndex.Load()
	newCommittedIndex := r.getNewCommittedIndex(req.CommittedIndex)
	if newCommittedIndex > oldCommittedIndex {
		r.committedIndex.Store(newCommittedIndex)
		if r.opts.OnCommit != nil {
			r.opts.OnCommit(oldCommittedIndex, newCommittedIndex)
		}
	}

	count, err := r.requestSyncLogs(req)
	if err != nil {
		return err
	}
	// 如果同步到的日志大于0，并小于同步limit的限制，则立马发起再次同步（最后一次同步的主要目的是告诉领导节点我已经达到最新了）
	// 如果同步到的数量大于等于limit的数量，说明本节点落后日志太多，这里不需要立马去同步了，因为无所谓了，等待主节点的同步通知即可（如果落后太多还一直去同步的话如果这种节点很多的话会导致占用大量带宽和cpu）
	// 落后太多就动态领导节点主动发起同步通知，再去同步
	if count > 0 && count < int(r.opts.SyncLimit) {
		_, err = r.requestSyncLogs(req) // 这次请求的主要目的，告诉领导我已经同步完最新日志
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *RawReplica) getNewCommittedIndex(leaderCommittedIndex uint64) uint64 {
	if leaderCommittedIndex > r.committedIndex.Load() {
		var newCommitted uint64
		if leaderCommittedIndex > r.lastEndLogIndex.Load() {
			newCommitted = r.lastEndLogIndex.Load()
		} else {
			newCommitted = leaderCommittedIndex
		}
		return newCommitted

	}
	return r.committedIndex.Load()
}

func (r *RawReplica) requestSyncLogs(req *SyncNotify) (int, error) {
	leaderID := r.leaderID.Load()

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

	for _, lg := range resp.Logs {
		err = r.appendLog(lg)
		if err != nil {
			r.Panic("append log failed", zap.Error(err))
		}
	}
	return len(resp.Logs), nil
}
