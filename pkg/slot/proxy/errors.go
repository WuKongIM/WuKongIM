package proxy

import "strings"

var (
	// ErrNoLeader indicates that no Slot leader is currently available.
	ErrNoLeader = &routeError{
		message: "slot/proxy: no leader",
		aliases: []string{
			"raftcluster: no leader for slot",
			"cluster: no slot leader",
			"cluster/routing: no slot leader",
		},
	}
	// ErrNotLeader indicates that the target node is not the Slot leader.
	ErrNotLeader = &routeError{
		message: "slot/proxy: not leader",
		aliases: []string{
			"raftcluster: not leader",
			"cluster: not leader",
			"cluster/propose: not leader",
		},
	}
	// ErrSlotNotFound indicates that the requested physical Slot does not exist.
	ErrSlotNotFound = &routeError{
		message: "slot/proxy: slot not found",
		aliases: []string{
			"raftcluster: slot not found",
			"cluster: slot not found",
		},
	}
)

var (
	errNoLeader     = ErrNoLeader
	errNotLeader    = ErrNotLeader
	errSlotNotFound = ErrSlotNotFound
)

type routeError struct {
	message string
	aliases []string
}

func (e *routeError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *routeError) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	msg := target.Error()
	if msg == e.message {
		return true
	}
	for _, alias := range e.aliases {
		if msg == alias {
			return true
		}
	}
	return false
}

func isNoLeader(err error) bool {
	return routeErrorMatches(err, ErrNoLeader)
}

func isNotLeader(err error) bool {
	return routeErrorMatches(err, ErrNotLeader)
}

func isSlotNotFound(err error) bool {
	return routeErrorMatches(err, ErrSlotNotFound)
}

func routeErrorMatches(err error, sentinel *routeError) bool {
	if err == nil || sentinel == nil {
		return false
	}
	if err == sentinel || sentinel.Is(err) {
		return true
	}
	msg := err.Error()
	if msg == sentinel.message {
		return true
	}
	for _, alias := range sentinel.aliases {
		if msg == alias || strings.Contains(msg, alias) {
			return true
		}
	}
	return false
}
