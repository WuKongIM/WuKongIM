package meta

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
)

// MessageUpdateImport is an offline transfer row. Table selects exactly one of
// the four registered edit tables; Row uses that table's inspected JSON fields.
type MessageUpdateImport struct {
	Table string          `json:"table"`
	Row   json.RawMessage `json:"row"`
}

// ValidateMessageUpdateImport decodes an exact bounded row before any writes.
func ValidateMessageUpdateImport(in MessageUpdateImport) (ChannelKey, error) {
	_, key, err := decodeMessageUpdateImport(in)
	return key, err
}
func decodeMessageUpdateImport(in MessageUpdateImport) (any, ChannelKey, error) {
	var row any
	switch in.Table {
	case "message_update", "message_update_pending":
		row = &MessageUpdate{}
	case "message_update_head":
		row = &MessageUpdateHead{}
	case "message_update_request":
		row = &MessageUpdateRequest{}
	default:
		return nil, ChannelKey{}, ErrInvalidArgument
	}
	if len(in.Row) > 2*MaxMessageUpdatePayload {
		return nil, ChannelKey{}, ErrInvalidArgument
	}
	decoder := json.NewDecoder(bytes.NewReader(in.Row))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(row); err != nil {
		return nil, ChannelKey{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ChannelKey{}, ErrInvalidArgument
	}
	var key ChannelKey
	switch r := row.(type) {
	case *MessageUpdate:
		key = ChannelKey{ChannelID: r.ChannelID, ChannelType: r.ChannelType}
		if r.MessageID == 0 || r.MessageSeq == 0 || r.Version == 0 || r.UpdateSeq == 0 || r.Version > r.UpdateSeq || r.UpdatedAtMS < 0 || len(r.Payload) > MaxMessageUpdatePayload || len(r.PendingAfterUID) > maxKeyStringLen {
			return nil, key, ErrInvalidArgument
		}
		if in.Table == "message_update" && (r.Pending != 0 || r.PendingAfterUID != "" || len(r.Payload) == 0) {
			return nil, key, ErrInvalidArgument
		}
		if in.Table == "message_update_pending" && (r.Pending != 1 || len(r.Payload) != 0) {
			return nil, key, ErrInvalidArgument
		}
	case *MessageUpdateHead:
		key = ChannelKey{ChannelID: r.ChannelID, ChannelType: r.ChannelType}
		if r.Generation == "" || len(r.Generation) > 128 || len(r.ReplicaSet) > 8192 {
			return nil, key, ErrInvalidArgument
		}
	case *MessageUpdateRequest:
		key = ChannelKey{ChannelID: r.ChannelID, ChannelType: r.ChannelType}
		if r.MessageID == 0 || r.MessageSeq == 0 || r.Version == 0 || r.UpdateSeq == 0 || r.Version > r.UpdateSeq || r.RequestID == "" || len(r.RequestID) > 128 || len(r.Digest) != 64 || r.UpdatedAtMS < 0 {
			return nil, key, ErrInvalidArgument
		}
		if _, err := hex.DecodeString(r.Digest); err != nil {
			return nil, key, ErrInvalidArgument
		}
	}
	return row, key, validateChannelKey(key)
}

// ImportMessageUpdate installs an exact offline projection, not a business
// mutation. Callers must quiesce the store and import heads, payloads, requests,
// then pending checkpoints. Parent identities/versions are checked before write.
func (db *MetaDB) ImportMessageUpdate(ctx context.Context, hs HashSlot, in MessageUpdateImport) error {
	row, key, err := decodeMessageUpdateImport(in)
	if err != nil {
		return err
	}
	shard := db.HashSlot(hs)
	cpk := KeyParts{String(key.ChannelID), Int64Ordered(key.ChannelType)}
	var head MessageUpdateHead
	if in.Table != "message_update_head" {
		head, err = requiredMessageUpdateImportRow(messageUpdateHeadTable, ctx, shard, cpk)
		if err != nil {
			return err
		}
	}
	switch r := row.(type) {
	case *MessageUpdateHead:
		return messageUpdateHeadTable.Upsert(ctx, shard, *r)
	case *MessageUpdate:
		if r.UpdateSeq > head.UpdateSeq {
			return ErrInvalidArgument
		}
		if in.Table == "message_update" {
			// Equal sequence positions for different targets would make incremental cursors skip rows.
			peers, _, _, e := messageUpdateTable.ScanIndex(ctx, shard, 2, append(append(KeyParts(nil), cpk...), Uint64(r.UpdateSeq)), nil, 2)
			if e != nil {
				return e
			}
			for _, peer := range peers {
				if peer.MessageID != r.MessageID {
					return ErrInvalidArgument
				}
			}
			return messageUpdateTable.Upsert(ctx, shard, *r)
		}
		latest, e := requiredMessageUpdateImportRow(messageUpdateTable, ctx, shard, append(cpk, Uint64(r.MessageID)))
		if e != nil {
			return e
		}
		if latest.MessageSeq != r.MessageSeq || latest.Version != r.Version || latest.UpdateSeq != r.UpdateSeq {
			return ErrInvalidArgument
		}
		return messageUpdatePendingTable.Upsert(ctx, shard, *r)
	case *MessageUpdateRequest:
		latest, e := requiredMessageUpdateImportRow(messageUpdateTable, ctx, shard, append(cpk, Uint64(r.MessageID)))
		if e != nil {
			return e
		}
		if r.MessageSeq != latest.MessageSeq || r.Version > latest.Version || r.UpdateSeq > head.UpdateSeq {
			return ErrInvalidArgument
		}
		return messageUpdateRequestTable.Upsert(ctx, shard, *r)
	}
	return ErrInvalidArgument
}

func requiredMessageUpdateImportRow[R any](table Table[R], ctx context.Context, shard *Shard, pk KeyParts) (R, error) {
	row, found, err := table.Get(ctx, shard, pk)
	if err == nil && !found {
		err = ErrNotFound
	}
	return row, err
}
