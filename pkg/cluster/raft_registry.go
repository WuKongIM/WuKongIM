package cluster

import "fmt"

type INodeRegistry interface {
	Close() error
	Add(shardID uint64, replicaID uint64, url string)
	Remove(shardID uint64, replicaID uint64)
	RemoveShard(shardID uint64)
	Resolve(shardID uint64, replicaID uint64) (string, string, error)
}

type NodeRegistry struct {
}

func NewNodeRegistry() *NodeRegistry {

	return &NodeRegistry{}
}

func (n *NodeRegistry) Add(shardID uint64, replicaID uint64, target string) {
	fmt.Println("NodeRegistry---Add....")
}

func (n *NodeRegistry) Remove(shardID uint64, replicaID uint64) {
	fmt.Println("NodeRegistry---Remove....")
}

func (n *NodeRegistry) RemoveShard(shardID uint64) {
	fmt.Println("NodeRegistry---RemoveShard....")
}

func (n *NodeRegistry) Resolve(shardID uint64, replicaID uint64) (string, string, error) {
	fmt.Println("NodeRegistry---Resolve....")
	return "", "", nil
}

func (n *NodeRegistry) Close() error {
	fmt.Println("NodeRegistry---Close....")
	return nil
}
