package fsm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

const (
	cmdTypeCreateChannelMigrationTask     uint8 = 30
	cmdTypeClaimChannelMigrationTask      uint8 = 31
	cmdTypeAdvanceChannelMigrationTask    uint8 = 32
	cmdTypeSetChannelWriteFence           uint8 = 33
	cmdTypeResetChannelWriteFence         uint8 = 34
	cmdTypeCommitChannelLeaderTransfer    uint8 = 35
	cmdTypeAddChannelLearner              uint8 = 36
	cmdTypePromoteLearnerAndRemoveReplica uint8 = 37
	cmdTypeClearChannelWriteFence         uint8 = 38
	cmdTypeAbortChannelMigration          uint8 = 39
	tagChannelMigrationCommandPayload     uint8 = 1
)

type createChannelMigrationTaskCmd struct {
	task metadb.ChannelMigrationTask
}

func (c *createChannelMigrationTaskCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.CreateChannelMigrationTask(hashSlot, c.task)
}

type claimChannelMigrationTaskCmd struct {
	req metadb.ChannelMigrationTaskClaim
}

func (c *claimChannelMigrationTaskCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.ClaimChannelMigrationTask(hashSlot, c.req)
}

type advanceChannelMigrationTaskCmd struct {
	req metadb.ChannelMigrationTaskAdvance
}

func (c *advanceChannelMigrationTaskCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.AdvanceChannelMigrationTask(hashSlot, c.req)
}

type setChannelWriteFenceCmd struct {
	req metadb.ChannelMigrationFenceRequest
}

func (c *setChannelWriteFenceCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.SetChannelWriteFence(hashSlot, c.req)
}

type resetChannelWriteFenceToPreCutoverCmd struct {
	req metadb.ChannelMigrationResetFenceRequest
}

func (c *resetChannelWriteFenceToPreCutoverCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.ResetChannelWriteFenceToPreCutover(hashSlot, c.req)
}

type commitChannelLeaderTransferCmd struct {
	req metadb.ChannelMigrationLeaderTransferRequest
}

func (c *commitChannelLeaderTransferCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.CommitChannelLeaderTransfer(hashSlot, c.req)
}

type addChannelLearnerCmd struct {
	req metadb.ChannelMigrationAddLearnerRequest
}

func (c *addChannelLearnerCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.AddChannelLearner(hashSlot, c.req)
}

type promoteLearnerAndRemoveReplicaCmd struct {
	req metadb.ChannelMigrationPromoteLearnerRequest
}

func (c *promoteLearnerAndRemoveReplicaCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.PromoteLearnerAndRemoveReplica(hashSlot, c.req)
}

type clearChannelWriteFenceCmd struct {
	req metadb.ChannelMigrationClearFenceRequest
}

func (c *clearChannelWriteFenceCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.ClearChannelWriteFence(hashSlot, c.req)
}

type abortChannelMigrationCmd struct {
	req metadb.ChannelMigrationAbortRequest
}

func (c *abortChannelMigrationCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.AbortChannelMigration(hashSlot, c.req)
}

// EncodeCreateChannelMigrationTaskCommand encodes a durable migration task create command.
func EncodeCreateChannelMigrationTaskCommand(task metadb.ChannelMigrationTask) []byte {
	return encodeChannelMigrationJSONCommand(cmdTypeCreateChannelMigrationTask, task)
}

// EncodeClaimChannelMigrationTaskCommand encodes a guarded owner-claim command.
func EncodeClaimChannelMigrationTaskCommand(req metadb.ChannelMigrationTaskClaim) []byte {
	return encodeChannelMigrationJSONCommand(cmdTypeClaimChannelMigrationTask, req)
}

// EncodeAdvanceChannelMigrationTaskCommand encodes a guarded task-only advance command.
func EncodeAdvanceChannelMigrationTaskCommand(req metadb.ChannelMigrationTaskAdvance) []byte {
	return encodeChannelMigrationJSONCommand(cmdTypeAdvanceChannelMigrationTask, req)
}

// EncodeSetChannelWriteFenceCommand encodes an atomic fence set command.
func EncodeSetChannelWriteFenceCommand(req metadb.ChannelMigrationFenceRequest) []byte {
	return encodeChannelMigrationJSONCommand(cmdTypeSetChannelWriteFence, req)
}

// EncodeResetChannelWriteFenceToPreCutoverCommand encodes an expired-fence recovery command.
func EncodeResetChannelWriteFenceToPreCutoverCommand(req metadb.ChannelMigrationResetFenceRequest) []byte {
	return encodeChannelMigrationJSONCommand(cmdTypeResetChannelWriteFence, req)
}

// EncodeCommitChannelLeaderTransferCommand encodes an atomic leader transfer commit command.
func EncodeCommitChannelLeaderTransferCommand(req metadb.ChannelMigrationLeaderTransferRequest) []byte {
	return encodeChannelMigrationJSONCommand(cmdTypeCommitChannelLeaderTransfer, req)
}

// EncodeAddChannelLearnerCommand encodes an atomic learner add command.
func EncodeAddChannelLearnerCommand(req metadb.ChannelMigrationAddLearnerRequest) []byte {
	return encodeChannelMigrationJSONCommand(cmdTypeAddChannelLearner, req)
}

// EncodePromoteLearnerAndRemoveReplicaCommand encodes an atomic learner promote/remove command.
func EncodePromoteLearnerAndRemoveReplicaCommand(req metadb.ChannelMigrationPromoteLearnerRequest) []byte {
	return encodeChannelMigrationJSONCommand(cmdTypePromoteLearnerAndRemoveReplica, req)
}

// EncodeClearChannelWriteFenceCommand encodes an atomic write-fence clear command.
func EncodeClearChannelWriteFenceCommand(req metadb.ChannelMigrationClearFenceRequest) []byte {
	return encodeChannelMigrationJSONCommand(cmdTypeClearChannelWriteFence, req)
}

// EncodeAbortChannelMigrationCommand encodes an atomic migration abort command.
func EncodeAbortChannelMigrationCommand(req metadb.ChannelMigrationAbortRequest) []byte {
	return encodeChannelMigrationJSONCommand(cmdTypeAbortChannelMigration, req)
}

func encodeChannelMigrationJSONCommand(cmdType uint8, payload any) []byte {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	data := []byte{commandVersion, cmdType}
	return appendBytesTLVField(data, tagChannelMigrationCommandPayload, raw)
}

func decodeCreateChannelMigrationTask(data []byte) (command, error) {
	var task metadb.ChannelMigrationTask
	if err := decodeChannelMigrationJSONPayload(data, &task); err != nil {
		return nil, err
	}
	return &createChannelMigrationTaskCmd{task: task}, nil
}

func decodeClaimChannelMigrationTask(data []byte) (command, error) {
	var req metadb.ChannelMigrationTaskClaim
	if err := decodeChannelMigrationJSONPayload(data, &req); err != nil {
		return nil, err
	}
	return &claimChannelMigrationTaskCmd{req: req}, nil
}

func decodeAdvanceChannelMigrationTask(data []byte) (command, error) {
	var req metadb.ChannelMigrationTaskAdvance
	if err := decodeChannelMigrationJSONPayload(data, &req); err != nil {
		return nil, err
	}
	return &advanceChannelMigrationTaskCmd{req: req}, nil
}

func decodeSetChannelWriteFence(data []byte) (command, error) {
	var req metadb.ChannelMigrationFenceRequest
	if err := decodeChannelMigrationJSONPayload(data, &req); err != nil {
		return nil, err
	}
	return &setChannelWriteFenceCmd{req: req}, nil
}

func decodeResetChannelWriteFence(data []byte) (command, error) {
	var req metadb.ChannelMigrationResetFenceRequest
	if err := decodeChannelMigrationJSONPayload(data, &req); err != nil {
		return nil, err
	}
	return &resetChannelWriteFenceToPreCutoverCmd{req: req}, nil
}

func decodeCommitChannelLeaderTransfer(data []byte) (command, error) {
	var req metadb.ChannelMigrationLeaderTransferRequest
	if err := decodeChannelMigrationJSONPayload(data, &req); err != nil {
		return nil, err
	}
	return &commitChannelLeaderTransferCmd{req: req}, nil
}

func decodeAddChannelLearner(data []byte) (command, error) {
	var req metadb.ChannelMigrationAddLearnerRequest
	if err := decodeChannelMigrationJSONPayload(data, &req); err != nil {
		return nil, err
	}
	return &addChannelLearnerCmd{req: req}, nil
}

func decodePromoteLearnerAndRemoveReplica(data []byte) (command, error) {
	var req metadb.ChannelMigrationPromoteLearnerRequest
	if err := decodeChannelMigrationJSONPayload(data, &req); err != nil {
		return nil, err
	}
	return &promoteLearnerAndRemoveReplicaCmd{req: req}, nil
}

func decodeClearChannelWriteFence(data []byte) (command, error) {
	var req metadb.ChannelMigrationClearFenceRequest
	if err := decodeChannelMigrationJSONPayload(data, &req); err != nil {
		return nil, err
	}
	return &clearChannelWriteFenceCmd{req: req}, nil
}

func decodeAbortChannelMigration(data []byte) (command, error) {
	var req metadb.ChannelMigrationAbortRequest
	if err := decodeChannelMigrationJSONPayload(data, &req); err != nil {
		return nil, err
	}
	return &abortChannelMigrationCmd{req: req}, nil
}

func decodeChannelMigrationJSONPayload(data []byte, dst any) error {
	payload, ok, err := findSingleChannelMigrationJSONPayload(data)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: missing channel migration payload", metadb.ErrCorruptValue)
	}
	if err := rejectDuplicateJSONObjectKeys(payload); err != nil {
		return fmt.Errorf("%w: invalid channel migration payload: %v", metadb.ErrCorruptValue, err)
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: invalid channel migration payload: %v", metadb.ErrCorruptValue, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return fmt.Errorf("%w: invalid channel migration payload: %v", metadb.ErrCorruptValue, err)
	}
	return nil
}

type jsonObjectScope struct {
	object    bool
	expectKey bool
	keys      map[string]struct{}
}

func rejectDuplicateJSONObjectKeys(payload []byte) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	var stack []jsonObjectScope
	markValueConsumed := func() {
		if len(stack) == 0 {
			return
		}
		top := &stack[len(stack)-1]
		if top.object && !top.expectKey {
			top.expectKey = true
		}
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch value := tok.(type) {
		case json.Delim:
			switch value {
			case '{':
				stack = append(stack, jsonObjectScope{
					object:    true,
					expectKey: true,
					keys:      make(map[string]struct{}),
				})
			case '[':
				stack = append(stack, jsonObjectScope{})
			case '}', ']':
				if len(stack) == 0 {
					return fmt.Errorf("unexpected JSON delimiter %q", value)
				}
				stack = stack[:len(stack)-1]
				markValueConsumed()
			}
		case string:
			if len(stack) > 0 {
				top := &stack[len(stack)-1]
				if top.object && top.expectKey {
					if _, exists := top.keys[value]; exists {
						return fmt.Errorf("duplicate JSON key %q", value)
					}
					top.keys[value] = struct{}{}
					top.expectKey = false
					continue
				}
			}
			markValueConsumed()
		default:
			markValueConsumed()
		}
	}
	if len(stack) != 0 {
		return fmt.Errorf("unterminated JSON object")
	}
	return nil
}

func findSingleChannelMigrationJSONPayload(data []byte) ([]byte, bool, error) {
	var payload []byte
	var found bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, false, err
		}
		off += n
		if tag != tagChannelMigrationCommandPayload {
			continue
		}
		if found {
			return nil, false, fmt.Errorf("%w: duplicate channel migration payload", metadb.ErrCorruptValue)
		}
		payload = append([]byte(nil), value...)
		found = true
	}
	return payload, found, nil
}
