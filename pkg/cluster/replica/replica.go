package replica

import (
	"time"

	"github.com/lni/goutils/syncutil"
)

type Replica struct {
	notifyChan chan struct{}
	syncChan   chan *SyncNotify

	rawReplica *RawReplica
	stopper    *syncutil.Stopper
}

func New(nodeID uint64, shardNo string, optList ...Option) *Replica {
	return &Replica{
		notifyChan: make(chan struct{}, 100),
		syncChan:   make(chan *SyncNotify, 100),
		rawReplica: NewRawReplica(nodeID, shardNo, optList...),
		stopper:    syncutil.NewStopper(),
	}
}

func (r *Replica) Start() error {
	err := r.rawReplica.Open()
	if err != nil {
		return err
	}
	r.stopper.RunWorker(r.loop)
	return nil
}

func (r *Replica) Stop() {
	r.stopper.Stop()
	r.rawReplica.Close()
}

func (r *Replica) IsLeader() bool {
	return r.rawReplica.IsLeader()
}

func (r *Replica) SetLeaderID(id uint64) {
	r.rawReplica.SetLeaderID(id)
}

func (r *Replica) AppendLog(lg Log) error {
	err := r.rawReplica.AppendLogOnlyLocal(lg)
	if err != nil {
		return err
	}
	r.notifyChan <- struct{}{} // 通知其他副本去同步
	return nil
}

func (r *Replica) TriggerHandleSyncNotify(req *SyncNotify) {
	select {
	case r.syncChan <- req:
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

	tick := time.NewTicker(r.rawReplica.opts.SyncCheckInterval)
	for {
		select {
		case <-r.notifyChan:
			// 取出所有的，因为每次只执行一次
			for {
				select {
				case <-r.notifyChan:
				default:
					goto toNotify
				}
			}
		toNotify:
			r.toNotifySync() // 通知副本去同步
		case req := <-r.syncChan: // 同步 TODO: 这里可以做请求合并
			r.rawReplica.HandleSyncNotify(req)
		case <-tick.C:
			if r.rawReplica.IsLeader() {
				r.rawReplica.TriggerSyncIfNeed()
			}

		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Replica) toNotifySync() {
	needSync, _ := r.rawReplica.NotifySync(r.rawReplica.opts.Replicas)
	if needSync {
		r.stopper.RunWorker(r.notifyDelay)
	}

}

func (r Replica) notifyDelay() {
	time.Sleep(time.Millisecond * 200)
	r.notifyChan <- struct{}{}
}
