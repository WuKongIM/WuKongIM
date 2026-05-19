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
