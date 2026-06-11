package message

import "context"

const channelTypePerson uint8 = 1

// Send delegates one send command to the configured channel append submitter.
func (a *App) Send(ctx context.Context, cmd SendCommand) (SendResult, error) {
	if a == nil || a.submitter == nil {
		return SendResult{}, ErrRouteNotReady
	}
	return a.submitter.Send(ctx, cmd)
}

// SendBatch delegates send commands to the configured channel append submitter.
func (a *App) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	if a == nil || a.submitter == nil {
		results := make([]SendBatchItemResult, len(items))
		for i := range results {
			results[i].Err = ErrRouteNotReady
		}
		return results
	}
	return a.submitter.SendBatch(items)
}
