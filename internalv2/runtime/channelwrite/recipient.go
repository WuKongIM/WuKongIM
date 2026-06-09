package channelwrite

import (
	"context"
	"errors"
	"strings"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
)

func dispatchCommittedRecipients(ctx context.Context, event CommittedEnvelope, ports commitPorts) error {
	if ports.recipientRouter == nil {
		return nil
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if len(event.MessageScopedUIDs) > 0 {
		return dispatchRecipientSet(ctx, event, recipientsFromUIDs(event.MessageScopedUIDs), ports)
	}
	if event.ChannelType == channelTypePerson {
		left, right, err := runtimechannelid.DecodePersonChannel(event.ChannelID)
		if err != nil {
			return err
		}
		return dispatchRecipientSet(ctx, event, []Recipient{{UID: left}, {UID: right}}, ports)
	}
	return dispatchSubscriberPages(ctx, event, ports)
}

func dispatchSubscriberPages(ctx context.Context, event CommittedEnvelope, ports commitPorts) error {
	if ports.subscribers == nil {
		return nil
	}
	pageSize := boundedPositive(ports.subscriberPageSize, defaultSubscriberPageSize)
	cursor := ""
	for {
		previousCursor := cursor
		if err := contextErr(ctx); err != nil {
			return err
		}
		page, err := ports.subscribers.NextSubscriberPage(ctx, SubscriberPageRequest{
			ChannelID: ChannelID{ID: event.ChannelID, Type: event.ChannelType},
			Cursor:    cursor,
			Limit:     pageSize,
		})
		if err != nil {
			return err
		}
		if !page.Done && (page.Cursor == "" || page.Cursor == previousCursor) {
			return ErrInvalidSubscriberCursor
		}
		if err := dispatchRecipientSet(ctx, event, page.Recipients, ports); err != nil {
			return err
		}
		if page.Done {
			return nil
		}
		cursor = page.Cursor
	}
}

func dispatchRecipientSet(ctx context.Context, event CommittedEnvelope, recipients []Recipient, ports commitPorts) error {
	if ports.recipientRouter == nil || len(recipients) == 0 {
		return nil
	}
	if ports.recipientAuthorityResolver == nil {
		return errors.New("channelwrite: recipient authority resolver required")
	}
	batchSize := boundedPositive(ports.recipientBatchSize, defaultRecipientBatchSize)
	grouped := make(map[RecipientAuthorityTarget][]Recipient)
	order := make([]RecipientAuthorityTarget, 0)
	for _, recipient := range recipients {
		uid := strings.TrimSpace(recipient.UID)
		if uid == "" {
			continue
		}
		recipient.UID = uid
		target, err := ports.recipientAuthorityResolver.ResolveRecipientAuthority(ctx, uid)
		if err != nil {
			return err
		}
		if err := target.Validate(); err != nil {
			return ErrRouteNotReady
		}
		if _, ok := grouped[target]; !ok {
			order = append(order, target)
		}
		grouped[target] = append(grouped[target], recipient)
	}
	for _, target := range order {
		recipients := grouped[target]
		for len(recipients) > 0 {
			n := batchSize
			if n > len(recipients) {
				n = len(recipients)
			}
			batch := RecipientBatch{
				Event:      event.Clone(),
				Recipients: append([]Recipient(nil), recipients[:n]...),
			}
			if err := ports.recipientRouter.DispatchRecipientBatch(ctx, target, batch); err != nil {
				return err
			}
			recipients = recipients[n:]
		}
	}
	return nil
}

func recipientsFromUIDs(uids []string) []Recipient {
	out := make([]Recipient, 0, len(uids))
	for _, uid := range uids {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		out = append(out, Recipient{UID: uid})
	}
	return out
}

func boundedPositive(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
