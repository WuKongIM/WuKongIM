package channel

import "errors"

var (
	ErrNoAllowVoteNode = errors.New("no allow vote node")
	ErrNoLeader        = errors.New("no leader")
)
