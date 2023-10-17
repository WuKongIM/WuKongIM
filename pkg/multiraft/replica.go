package multiraft

type Replica struct {
	opts *ReplicaOptions
}

func NewReplica(opts *ReplicaOptions) *Replica {
	return &Replica{
		opts: opts,
	}
}

func (r *Replica) Start() error {
	return nil
}

func (r *Replica) Stop() {

}
