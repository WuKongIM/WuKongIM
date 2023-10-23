package multiraft

import "sync"

type ReplicaManager struct {
	replicas    map[uint32]*Replica
	replicaLock sync.RWMutex
}

func NewReplicaManager() *ReplicaManager {
	return &ReplicaManager{
		replicas: make(map[uint32]*Replica),
	}
}

func (r *ReplicaManager) AddReplica(replicaID uint32, replica *Replica) error {
	r.replicaLock.Lock()
	defer r.replicaLock.Unlock()
	r.replicas[replicaID] = replica
	return nil
}

func (r *ReplicaManager) RemoveReplica(replicaID uint32) {
	r.replicaLock.Lock()
	defer r.replicaLock.Unlock()
	delete(r.replicas, replicaID)
}

func (r *ReplicaManager) GetReplica(replicaID uint32) *Replica {
	r.replicaLock.RLock()
	defer r.replicaLock.RUnlock()
	return r.replicas[replicaID]
}
