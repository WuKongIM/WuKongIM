

```go

// cluster 1
opts := cluster.NewOptions()
opts.NodeID = 1
opts.Addr = "tcp://127.0.0.1:11000"
cls := cluster.New(opts)
cls.Start()
defer cls.Stop()

// cluster 2
opts = cluster.NewOptions()
opts.NodeID = 2
opts.Addr = "tcp://127.0.0.1:12000"
opts.Join = "tcp://127.0.0.1:11000" // join cluster 1
cls = cluster.New(opts)
cls.Start()
defer cls.Stop()


```