package core

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
)

// CloseTransportAndWait never enters closeOnce or lifecycle callbacks, which
// may themselves depend on the caller's business operation. The transport's
// ordinary close notification performs that cleanup independently.
func (st *sessionState) CloseTransportAndWait(ctx context.Context, reason gt.CloseReason) error {
	if ctx == nil || st == nil {
		return transport.ErrCloseUnproved
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	closer, ok := st.conn.(transport.CloseWaiter)
	if !ok {
		return gt.ErrCloseProofUnsupported
	}
	fencer, ok := st.session.(session.OutboundFencer)
	if !ok {
		return gt.ErrCloseProofUnsupported
	}
	st.metaMu.Lock()
	if !st.closing {
		st.closing = true
		st.closeReasonValue = reason
	}
	st.metaMu.Unlock()
	fencer.FenceOutbound()
	if st.cancelRequestContext != nil {
		st.cancelRequestContext()
	}
	return closer.CloseAndWait(ctx)
}
