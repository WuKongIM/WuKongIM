package message

import "context"

const (
	channelTypePerson          uint8 = 1
	channelTypeGroup           uint8 = 2
	channelTypeCustomerService uint8 = 3
	channelTypeInfo            uint8 = 6
	channelTypeVisitors        uint8 = 10
	channelTypeAgent           uint8 = 11
)

// Send checks send permissions and delegates one allowed command to the configured channel append submitter.
func (a *App) Send(ctx context.Context, cmd SendCommand) (SendResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd, reason, err := a.checkSendPermission(ctx, cmd)
	if err != nil {
		return SendResult{Reason: reason}, err
	}
	if reason != ReasonSuccess {
		return SendResult{Reason: reason}, nil
	}
	if a == nil || a.submitter == nil {
		return SendResult{}, ErrRouteNotReady
	}
	return a.submitter.Send(ctx, cmd)
}

// SendBatch checks send permissions and delegates allowed commands to the configured channel append submitter.
func (a *App) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	results := make([]SendBatchItemResult, len(items))
	allowed := make([]SendBatchItem, 0, len(items))
	indexes := make([]int, 0, len(items))
	for i, item := range items {
		ctx := item.Context
		if ctx == nil {
			ctx = context.Background()
		}
		cmd, reason, err := a.checkSendPermission(ctx, item.Command)
		if err != nil {
			results[i] = SendBatchItemResult{Result: SendResult{Reason: reason}, Err: err}
			continue
		}
		if reason != ReasonSuccess {
			results[i] = SendBatchItemResult{Result: SendResult{Reason: reason}}
			continue
		}
		item.Command = cmd
		allowed = append(allowed, item)
		indexes = append(indexes, i)
	}
	if len(allowed) == 0 {
		return results
	}
	if a == nil || a.submitter == nil {
		for _, index := range indexes {
			results[index].Err = ErrRouteNotReady
		}
		return results
	}
	delegated := a.submitter.SendBatch(allowed)
	for i, result := range delegated {
		if i >= len(indexes) {
			break
		}
		results[indexes[i]] = result
	}
	return results
}
