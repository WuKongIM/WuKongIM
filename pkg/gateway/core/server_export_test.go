package core

// TestState exposes a session state to external package tests.
func (s *Server) TestState(listener string, connID uint64) *sessionState {
	return s.state(listener, connID)
}

// TestInboundCapacity returns retained inbound buffer capacity for fast-path tests.
func (st *sessionState) TestInboundCapacity() int {
	if st == nil {
		return 0
	}
	st.inboundMu.Lock()
	defer st.inboundMu.Unlock()
	return cap(st.inbound)
}

// TestAsyncRuntimeSendWorkers returns the active server async SEND worker count.
func (s *Server) TestAsyncRuntimeSendWorkers() int {
	runtime := s.asyncRuntime()
	if runtime == nil || runtime.send == nil {
		return 0
	}
	return runtime.send.workers
}

// TestAsyncRuntimeSendCapacity returns the active server async SEND capacity.
func (s *Server) TestAsyncRuntimeSendCapacity() int {
	runtime := s.asyncRuntime()
	if runtime == nil || runtime.send == nil {
		return 0
	}
	return runtime.send.totalCapacity()
}

// TestAsyncRuntimeAuthWorkers returns the active server async auth worker count.
func (s *Server) TestAsyncRuntimeAuthWorkers() int {
	runtime := s.asyncRuntime()
	if runtime == nil || runtime.auth == nil {
		return 0
	}
	return runtime.auth.workerCount()
}

// TestAsyncRuntimeAuthCapacity returns the active server async auth capacity.
func (s *Server) TestAsyncRuntimeAuthCapacity() int {
	runtime := s.asyncRuntime()
	if runtime == nil || runtime.auth == nil {
		return 0
	}
	return runtime.auth.totalCapacity()
}

// TestAsyncRuntimeActive reports whether the server has an active async runtime.
func (s *Server) TestAsyncRuntimeActive() bool {
	return s.asyncRuntime() != nil
}
