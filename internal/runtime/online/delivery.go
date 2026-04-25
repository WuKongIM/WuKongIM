package online

import "github.com/WuKongIM/WuKongIM/pkg/protocol/frame"

type LocalDelivery struct{}

func (LocalDelivery) Deliver(recipients []OnlineConn, f frame.Frame) error {
	var firstErr error
	for _, recipient := range recipients {
		if recipient.State == LocalRouteStateClosing {
			continue
		}
		if recipient.Session == nil {
			if firstErr == nil {
				firstErr = ErrInvalidConnection
			}
			continue
		}
		if err := recipient.Session.WriteFrame(f); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
