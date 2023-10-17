package multiraft

import (
	"context"

	"go.etcd.io/raft/v3/raftpb"
)

type Transporter interface {
	Stop() error
	Send(ctx context.Context, m raftpb.Message)
}
