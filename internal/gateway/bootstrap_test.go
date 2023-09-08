package gateway

// func TestBootstrap(t *testing.T) {

// 	s := wkserver.New("tcp://127.0.0.1:0")
// 	err := s.Start()
// 	assert.NoError(t, err)
// 	defer s.Stop()

// 	s.Route("/getclusterconfigset", func(c *wkserver.Context) {
// 		config := &pb.PartitionConfigSet{}
// 		config.Clusters = append(config.Clusters, &pb.ClusterConfig{
// 			ClusterID: 1,
// 			LeaderID:  1,
// 			Nodes: []*pb.NodeConfig{
// 				{
// 					NodeID:   1,
// 					NodeAddr: "tcp://127.0.0.1:8000",
// 				},
// 			},
// 		})
// 		data, _ := config.Marshal()

// 		c.Write(data)
// 	})

// 	dir := os.TempDir()

// 	fmt.Println(dir)

// 	addr := strings.ReplaceAll(s.Addr().String(), "tcp://", "")
// 	b := newBootstrap(1, addr, path.Join(dir, "cluster.json"))
// 	err = b.bootstrap()
// 	assert.NoError(t, err)

// }
