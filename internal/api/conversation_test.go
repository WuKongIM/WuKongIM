package api

// func TestSyncUserConversation(t *testing.T) {
// 	s := NewTestServer(t)
// 	err := s.Start()
// 	assert.NoError(t, err)
// 	defer func() {
// 		_ = s.Stop()
// 	}()

// 	s.MustWaitClusterReady(time.Second * 10)

// 	// new client 1
// 	cli1 := client.New(s.opts.External.TCPAddr, client.WithUID("u1"))
// 	err = cli1.Connect()
// 	assert.Nil(t, err)

// 	err = cli1.SendMessage(client.NewChannel("u2", 1), []byte("hello"))
// 	assert.Nil(t, err)

// 	time.Sleep(time.Second * 1)

// 	// 获取u1的最近会话列表
// 	w := httptest.NewRecorder()
// 	req, _ := http.NewRequest("POST", "/conversation/sync", bytes.NewReader([]byte(wkutil.ToJson(map[string]interface{}{
// 		"uid":       "u1",
// 		"msg_count": 10,
// 	}))))

// 	s.apiServer.r.ServeHTTP(w, req)

// 	var conversations []*syncUserConversationResp
// 	err = wkutil.ReadJSONByByte(w.Body.Bytes(), &conversations)
// 	assert.Nil(t, err)

// 	assert.Equal(t, 1, len(conversations))
// 	assert.Equal(t, "u2", conversations[0].ChannelId)
// 	assert.Equal(t, 0, conversations[0].Unread)

// 	// 获取u2的最近会话列表
// 	w = httptest.NewRecorder()
// 	req, _ = http.NewRequest("POST", "/conversation/sync", bytes.NewReader([]byte(wkutil.ToJson(map[string]interface{}{
// 		"uid":       "u2",
// 		"msg_count": 10,
// 	}))))

// 	s.apiServer.r.ServeHTTP(w, req)

// 	conversations = make([]*syncUserConversationResp, 0)
// 	err = wkutil.ReadJSONByByte(w.Body.Bytes(), &conversations)
// 	assert.Nil(t, err)

// 	assert.Equal(t, 1, len(conversations))
// 	assert.Equal(t, "u1", conversations[0].ChannelId)
// 	assert.Equal(t, 1, conversations[0].Unread)
// }
