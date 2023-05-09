package server

type DeliveryManager struct {
	s *Server
}

func NewDeliveryManager(s *Server) *DeliveryManager {
	return &DeliveryManager{
		s: s,
	}
}
