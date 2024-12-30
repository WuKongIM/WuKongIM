
```go
node := raft.NewNode(opt...)

// ready
ready := node.Ready()

// step
node.Step(m)

// switch config
node.SwitchConfig(cfg)
```