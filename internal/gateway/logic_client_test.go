package gateway

// func TestNodeClientAppend(t *testing.T) {
// 	s := wkserver.New("tcp://127.0.0.1:0")
// 	err := s.Start()
// 	assert.NoError(t, err)
// 	defer s.Stop()

// 	var w sync.WaitGroup
// 	w.Add(1)
// 	s.Route("/node/conn/write", func(c *wkserver.Context) {
// 		fmt.Println(string(c.Body()))
// 		c.WriteOk()
// 		w.Done()
// 	})

// 	opts := newNodeClientOptions()
// 	addr := strings.ReplaceAll(s.Addr().String(), "tcp://", "")
// 	opts.addr = addr
// 	nodecli := newNodeClient(opts)
// 	err = nodecli.start()
// 	assert.NoError(t, err)
// 	defer nodecli.stop()

// 	_, err = nodecli.append([]byte("test"))
// 	assert.NoError(t, err)

// 	w.Wait()
// }
