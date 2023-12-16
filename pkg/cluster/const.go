package cluster

import "fmt"

type SlotID uint32

type NodeID uint64

func (n NodeID) Uint64() uint64 {
	return uint64(n)
}

func (n NodeID) String() string {
	return fmt.Sprintf("%d", n)
}
