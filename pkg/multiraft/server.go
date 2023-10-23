package multiraft

import (
	"context"
	"fmt"
	"os"
	"path"
)

type Server struct {
	options        *Options
	replicaManager *ReplicaManager
}

func New(opts *Options) *Server {
	err := os.MkdirAll(opts.RootDir, os.ModePerm)
	if err != nil {
		panic(err)
	}
	if opts.Transporter == nil {
		opts.Transporter = NewDefaultTransporter(opts.PeerID, opts.Addr)
	}
	return &Server{
		options:        opts,
		replicaManager: NewReplicaManager(),
	}
}

func (s *Server) Start() error {

	s.options.Transporter.OnRaftMessage(func(m *RaftMessageReq) {
		fmt.Println("RaftMessageReq---->", m.ReplicaID)
		replica := s.replicaManager.GetReplica(m.ReplicaID)
		if replica != nil {
			go replica.OnRaftMessage(m)
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
	err = s.options.Transporter.Start()
	return err
}

func (s *Server) Stop() {
	s.options.ReplicaRaftStorage.Close()
	s.options.Transporter.Stop()
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
