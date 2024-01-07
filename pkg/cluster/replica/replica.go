package replica

import (
	"time"

	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type Replica struct {
	notifyChan chan struct{}
	syncChan   chan struct{}

	rawReplica *RawReplica
	stopper    *syncutil.Stopper
}

func New(nodeID uint64, shardNo string, optList ...Option) *Replica {
	return &Replica{
		notifyChan: make(chan struct{}, 100),
		syncChan:   make(chan struct{}, 100),
		rawReplica: NewRawReplica(nodeID, shardNo, optList...),
		stopper:    syncutil.NewStopper(),
	}
}

func (r *Replica) Start() error {
	r.stopper.RunWorker(r.loop)
	return nil
}

func (r *Replica) Stop() {
	r.stopper.Stop()
}

func (r *Replica) IsLeader() bool {
	return r.rawReplica.IsLeader()
}

func (r *Replica) SetLeaderID(id uint64) {
	r.rawReplica.SetLeaderID(id)
}

func (r *Replica) Propose(data []byte) error {
	err := r.rawReplica.ProposeOnlyLocal(data)
	if err != nil {
		return err
	}
	r.notifyChan <- struct{}{} // 通知其他副本去同步

	err = r.rawReplica.CheckAndCommitLogs()
	if err != nil {
		r.rawReplica.Panic("CheckAndCommitLogs failed", zap.Error(err))
	}

	return nil
}

func (r *Replica) AppendLog(lg Log) error {
	err := r.rawReplica.AppendLogOnlyLocal(lg)
	if err != nil {
		return err
	}
	r.notifyChan <- struct{}{} // 通知其他副本去同步
	return nil
}

func (r *Replica) TriggerHandleSyncNotify() {
	select {
	case r.syncChan <- struct{}{}:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *Replica) LastIndex() (uint64, error) {

	return r.rawReplica.LastIndex()
}

func (r *Replica) SetReplicas(replicas []uint64) {
	r.rawReplica.SetReplicas(replicas)
}

func (r *Replica) GetLogs(startLogIndex uint64, limit uint32) ([]Log, error) {
	return r.rawReplica.GetLogs(startLogIndex, limit)
}

func (r *Replica) SyncLogs(nodeID uint64, startLogIndex uint64, limit uint32) ([]Log, error) {
	return r.rawReplica.SyncLogs(nodeID, startLogIndex, limit)
}

func (r *Replica) loop() {

	tick := time.NewTicker(r.rawReplica.opts.CheckInterval)
	for {
		select {
		case <-r.notifyChan:
			if !r.IsLeader() { // 非领导节点不能操作
				continue
			}
			// 取出所有的，因为每次只执行一次
			for {
				select {
				case <-r.notifyChan:
				default:
					goto toNotify
				}
			}
		toNotify:
			r.sendNotifySync() // 通知副本去同步
		case <-r.syncChan: // 副本去同步主节点的日志
			if r.IsLeader() { // 主节点没有此操作
				continue
			}
			// 取出全部请求
			for {
				select {
				case <-r.syncChan:
				default:
					goto toHandleSyncNotify
				}
			}
		toHandleSyncNotify:
			_, _ = r.rawReplica.RequestSyncLogsAndNotifyLeaderIfNeed() // 同步日志

		case <-tick.C:
			if r.rawReplica.IsLeader() {
				r.rawReplica.TriggerSendNotifySyncIfNeed()
			}
			err := r.rawReplica.CheckAndCommitLogs() // 检查和提交日志
			if err != nil {
				r.rawReplica.Warn("CheckAndCommitLogs failed", zap.Error(err))
			}

		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Replica) sendNotifySync() {
	needSync, _ := r.rawReplica.SendNotifySync(r.rawReplica.opts.Replicas)
	if needSync {
		r.stopper.RunWorker(r.notifyDelay)
	}

}

func (r Replica) notifyDelay() {
	time.Sleep(time.Millisecond * 200)
	r.notifyChan <- struct{}{}
}
