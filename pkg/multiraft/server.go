package multiraft

import (
	"context"
	"fmt"
	"os"
	"path"
)

type Server struct {
	options            *Options
	replicaManager     *ReplicaManager
	defaultTransporter *DefaultTransporter
}

func New(opts *Options) *Server {
	err := os.MkdirAll(opts.RootDir, os.ModePerm)
	if err != nil {
		panic(err)
	}
	var defaultTransporter *DefaultTransporter
	if opts.Transporter == nil {
		defaultTransporter = NewDefaultTransporter(opts.PeerID, opts.Addr)
		opts.Transporter = defaultTransporter
	}
	if opts.ReplicaRaftStorage == nil {
		opts.ReplicaRaftStorage = NewReplicaBoltRaftStorage(path.Join(opts.RootDir, "raft.db"))
	}
	return &Server{
		options:            opts,
		defaultTransporter: defaultTransporter,
		replicaManager:     NewReplicaManager(),
	}
}

func (s *Server) Start() error {

	s.options.Transporter.OnRaftMessage(func(m *RaftMessageReq) {
		replica := s.replicaManager.GetReplica(m.ReplicaID)
		if replica != nil {
			replica.OnRaftMessage(m)
		}
	})

	err := s.options.ReplicaRaftStorage.Open()
	if err != nil {
		return err
	}
	if len(s.options.Peers) > 0 {
		for _, peer := range s.options.Peers {
			err = s.options.Transporter.AddPeer(peer)
			if err != nil {
				return err
			}
		}
	}
	if s.defaultTransporter != nil {
		err = s.defaultTransporter.Start()
		if err != nil {
			return err
		}
	}
	return err
}

func (s *Server) Stop() {
	replicas := s.replicaManager.GetReplicas()
	for _, replica := range replicas {
		replica.Stop()
	}
	if s.defaultTransporter != nil {
		s.defaultTransporter.Stop()

	}
	s.options.ReplicaRaftStorage.Close()

}

func (s *Server) StartReplica(replicaID uint32, opts *ReplicaOptions) (*Replica, error) {
	replica := s.replicaManager.GetReplica(replicaID)
	if replica == nil {
		opts.ReplicaRaftStorage = s.options.ReplicaRaftStorage
		opts.ReplicaID = replicaID
		opts.PeerID = s.options.PeerID
		opts.StateMachine = s.options.StateMachine
		opts.Transporter = s.options.Transporter
		opts.DataDir = path.Join(s.options.RootDir, fmt.Sprintf("%d", replicaID))
		leaderChange := opts.LeaderChange
		opts.LeaderChange = func(newLeaderID, oldLeaderID uint64) {
			if s.options.LeaderChange != nil {
				s.options.LeaderChange(replicaID, newLeaderID, oldLeaderID)
			}
			if leaderChange != nil {
				leaderChange(newLeaderID, oldLeaderID)
			}
		}
		replica = NewReplica(opts)
	}
	err := replica.Start()
	if err != nil {
		return nil, err
	}
	err = s.replicaManager.AddReplica(replicaID, replica)
	if err != nil {
		return nil, err
	}
	return replica, nil
}

func (s *Server) StopReplica(replicaID uint32) {
	replica := s.replicaManager.GetReplica(replicaID)
	if replica != nil {
		replica.Stop()
		s.replicaManager.RemoveReplica(replicaID)
	}
}

func (s *Server) Propose(ctx context.Context, replicaID uint32, data []byte) error {
	replica := s.replicaManager.GetReplica(replicaID)
	if replica != nil {
		return replica.Propose(ctx, data)
	}
	return fmt.Errorf("replica not found")
}

func (s *Server) GetReplica(replicaID uint32) *Replica {
	return s.replicaManager.GetReplica(replicaID)
}

func (s *Server) ExistReplica(replicaID uint32) bool {
	return s.replicaManager.GetReplica(replicaID) != nil
}
