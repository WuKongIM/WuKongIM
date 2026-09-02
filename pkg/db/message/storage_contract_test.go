package message

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestMessageDurableSystemValueCodecsRoundTrip(t *testing.T) {
	checkpoint := Checkpoint{Epoch: 7, LogStartOffset: 11, HW: 29}
	decodedCheckpoint, err := decodeCheckpoint(encodeCheckpoint(checkpoint))
	if err != nil || decodedCheckpoint != checkpoint {
		t.Fatalf("checkpoint round trip = %+v, %v; want %+v", decodedCheckpoint, err, checkpoint)
	}
	if err := validateCheckpoint(checkpoint); err != nil {
		t.Fatalf("valid checkpoint rejected: %v", err)
	}
	if err := validateCheckpoint(Checkpoint{LogStartOffset: 30, HW: 29}); !errors.Is(err, dberrors.ErrCorruptState) {
		t.Fatalf("invalid checkpoint error = %v, want ErrCorruptState", err)
	}
	for _, size := range []int{0, 23, 25} {
		if _, err := decodeCheckpoint(make([]byte, size)); !errors.Is(err, dberrors.ErrCorruptValue) {
			t.Fatalf("decodeCheckpoint(%d bytes) error = %v, want ErrCorruptValue", size, err)
		}
	}

	point := EpochPoint{Epoch: 8, StartOffset: 30}
	decodedPoint, err := decodeEpochPoint(encodeEpochPoint(point))
	if err != nil || decodedPoint != point {
		t.Fatalf("epoch point round trip = %+v, %v; want %+v", decodedPoint, err, point)
	}
	if _, err := decodeEpochPoint(make([]byte, 15)); !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("short epoch point error = %v, want ErrCorruptValue", err)
	}

	retention := RetentionState{LocalRetentionThroughSeq: 40, PhysicalRetentionThroughSeq: 35, RetainedMaxSeq: 45}
	decodedRetention, err := decodeRetentionState(encodeRetentionState(retention))
	if err != nil || decodedRetention != retention {
		t.Fatalf("retention round trip = %+v, %v; want %+v", decodedRetention, err, retention)
	}
	invalidRetention := []RetentionState{
		{RetainedMaxSeq: 1},
		{LocalRetentionThroughSeq: 2, PhysicalRetentionThroughSeq: 3, RetainedMaxSeq: 3},
		{LocalRetentionThroughSeq: 3, RetainedMaxSeq: 2},
	}
	for _, state := range invalidRetention {
		if err := validateRetentionState(state); !errors.Is(err, dberrors.ErrCorruptValue) {
			t.Fatalf("validateRetentionState(%+v) error = %v, want ErrCorruptValue", state, err)
		}
	}
	corruptRetention := encodeRetentionState(retention)
	corruptRetention[0]++
	if _, err := decodeRetentionState(corruptRetention); !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("unknown retention version error = %v, want ErrCorruptValue", err)
	}

	id := ChannelID{ID: "room-一", Type: 3}
	decodedID, err := decodeCatalogValue(encodeCatalogValue(id))
	if err != nil || decodedID != id {
		t.Fatalf("catalog value round trip = %+v, %v; want %+v", decodedID, err, id)
	}
	for _, value := range [][]byte{nil, {0, 1}, append(encodeCatalogValue(id), 4)} {
		if _, err := decodeCatalogValue(value); !errors.Is(err, dberrors.ErrCorruptValue) {
			t.Fatalf("decodeCatalogValue(%x) error = %v, want ErrCorruptValue", value, err)
		}
	}
}

func TestMessageDurableKeysRoundTripAndRejectCrossChannelKeys(t *testing.T) {
	key := ChannelKey("room-一:3")
	other := ChannelKey("room-two:3")

	rowKey := encodeMessageRowKey(key, 42, messageHeaderFamilyID)
	seq, family, ok := decodeMessageRowKey(key, rowKey)
	if !ok || seq != 42 || family != messageHeaderFamilyID {
		t.Fatalf("message row key decode = (%d, %d, %v)", seq, family, ok)
	}
	if _, _, ok := decodeMessageRowKey(other, rowKey); ok {
		t.Fatal("message row key decoded under another channel")
	}
	if _, _, ok := decodeMessageRowKey(key, rowKey[:len(rowKey)-1]); ok {
		t.Fatal("truncated message row key decoded")
	}

	clientKey := encodeMessageClientMsgNoIndexKey(key, "client-一", 42)
	if got, ok := decodeMessageClientMsgNoIndexSeq(key, "client-一", clientKey); !ok || got != 42 {
		t.Fatalf("client-message index decode = (%d, %v)", got, ok)
	}
	if _, ok := decodeMessageClientMsgNoIndexSeq(other, "client-一", clientKey); ok {
		t.Fatal("client-message index decoded under another channel")
	}

	senderKey := encodeMessageSenderSeqIndexKey(key, "user-一", 43)
	if got, ok := decodeMessageSenderSeqIndexSeq(key, "user-一", senderKey); !ok || got != 43 {
		t.Fatalf("sender index decode = (%d, %v)", got, ok)
	}
	if _, ok := decodeMessageSenderSeqIndexSeq(key, "different", senderKey); ok {
		t.Fatal("sender index decoded under another sender")
	}

	globalIDKey := encodeGlobalMessageIDIndexKey(99)
	if got, ok := decodeGlobalMessageIDIndexKey(globalIDKey); !ok || got != 99 {
		t.Fatalf("global message ID key decode = (%d, %v)", got, ok)
	}
	messageIDKey := encodeMessageIDIndexKey(key, 99)
	if got, ok := decodeMessageIDIndexKey(key, messageIDKey); !ok || got != 99 {
		t.Fatalf("channel message ID key decode = (%d, %v)", got, ok)
	}

	proposalLast := encodeProposalByLastKey(key, 55)
	if got, ok := decodeProposalByLastKey(key, proposalLast); !ok || got != 55 {
		t.Fatalf("proposal-last key decode = (%d, %v)", got, ok)
	}
	var commandID [32]byte
	for i := range commandID {
		commandID[i] = byte(i + 1)
	}
	proposalCommand := encodeProposalByCommandKey(key, commandID)
	if got, ok := decodeProposalByCommandKey(key, proposalCommand); !ok || got != commandID {
		t.Fatalf("proposal-command key decode = (%x, %v)", got, ok)
	}
	entryKey := encodeEntryIdentityKey(key, 56)
	if got, ok := decodeEntryIdentityKey(key, entryKey); !ok || got != 56 {
		t.Fatalf("entry identity key decode = (%d, %v)", got, ok)
	}

	catalogKey := encodeCatalogKey(key)
	if got, ok := decodeCatalogKey(catalogKey); !ok || got != key {
		t.Fatalf("catalog key decode = (%q, %v)", got, ok)
	}
	if _, ok := decodeCatalogKey(append(catalogKey, 0)); ok {
		t.Fatal("catalog key with trailing bytes decoded")
	}
	if _, ok := decodeCatalogKey(encodeCheckpointKey(key)); ok {
		t.Fatal("checkpoint key decoded as catalog key")
	}

	systemKeys := [][]byte{
		encodeRetentionStateKey(key), encodeCommittedCursorKey(key, "dispatch"), encodeCheckpointKey(key),
		encodeHistoryPointKey(key, EpochPoint{Epoch: 8, StartOffset: 30}), encodeSnapshotKey(key),
		proposalLast, proposalCommand, entryKey,
	}
	for i := range systemKeys {
		for j := i + 1; j < len(systemKeys); j++ {
			if bytes.Equal(systemKeys[i], systemKeys[j]) {
				t.Fatalf("durable system keys %d and %d collide", i, j)
			}
		}
	}
}

func TestEpochHistoryAdmissionAndKeyValueAgreement(t *testing.T) {
	points := []EpochPoint{{Epoch: 1, StartOffset: 0}, {Epoch: 2, StartOffset: 10}}
	tests := []struct {
		name  string
		point EpochPoint
		want  bool
		err   error
	}{
		{"first point", EpochPoint{Epoch: 1}, true, nil},
		{"advance epoch and offset", EpochPoint{Epoch: 3, StartOffset: 10}, true, nil},
		{"exact replay", EpochPoint{Epoch: 2, StartOffset: 10}, false, nil},
		{"zero epoch", EpochPoint{}, false, dberrors.ErrCorruptState},
		{"epoch regression", EpochPoint{Epoch: 1, StartOffset: 11}, false, dberrors.ErrCorruptState},
		{"offset regression", EpochPoint{Epoch: 3, StartOffset: 9}, false, dberrors.ErrCorruptState},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			current := points
			if tc.name == "first point" {
				current = nil
			}
			got, err := shouldAppendHistoryPoint(current, tc.point)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("shouldAppendHistoryPoint = (%v, %v), want (%v, %v)", got, err, tc.want, tc.err)
			}
		})
	}

	channelKey := ChannelKey("room:1")
	point := EpochPoint{Epoch: 4, StartOffset: 20}
	key := encodeHistoryPointKey(channelKey, point)
	got, err := decodeEpochPointFromKeyValue(channelKey, key, func() ([]byte, error) {
		return encodeEpochPoint(point), nil
	})
	if err != nil || got != point {
		t.Fatalf("history key/value decode = (%+v, %v), want %+v", got, err, point)
	}
	if _, err := decodeEpochPointFromKeyValue(channelKey, key[:len(key)-1], func() ([]byte, error) { return encodeEpochPoint(point), nil }); !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("truncated history key error = %v, want ErrCorruptValue", err)
	}
	mismatch := point
	mismatch.Epoch++
	if _, err := decodeEpochPointFromKeyValue(channelKey, key, func() ([]byte, error) { return encodeEpochPoint(mismatch), nil }); !errors.Is(err, dberrors.ErrCorruptState) {
		t.Fatalf("history key/value mismatch error = %v, want ErrCorruptState", err)
	}
	sentinel := errors.New("read failed")
	if _, err := decodeEpochPointFromKeyValue(channelKey, key, func() ([]byte, error) { return nil, sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("value reader error = %v, want sentinel", err)
	}
}

func TestMessageIndexValuesAndAppendKeyCacheAgreeWithCanonicalEncoding(t *testing.T) {
	row := normalizeMessageRow(messageRow{MessageSeq: 42, MessageID: 99, Payload: []byte("payload")})
	idempotency, err := encodeIdempotencyIndexValue(row)
	if err != nil {
		t.Fatalf("encode idempotency: %v", err)
	}
	hit, err := decodeIdempotencyIndexValue(idempotency)
	if err != nil || hit.MessageSeq != row.MessageSeq || hit.MessageID != row.MessageID || hit.Offset != row.MessageSeq-1 || hit.PayloadHash != row.PayloadHash {
		t.Fatalf("idempotency round trip = %+v, %v", hit, err)
	}
	if _, err := decodeIdempotencyIndexValue(idempotency[:len(idempotency)-1]); !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("short idempotency value error = %v, want ErrCorruptValue", err)
	}
	if err := writeIdempotencyIndexValue(make([]byte, idempotencyIndexValueLen-1), row); !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("wrong idempotency destination error = %v, want ErrInvalidArgument", err)
	}

	seqValue := encodeMessageIDIndexValue(42)
	if got, err := decodeMessageIDIndexValue(seqValue); err != nil || got != 42 {
		t.Fatalf("message ID index round trip = (%d, %v)", got, err)
	}
	if _, err := decodeMessageIDIndexValue(seqValue[:7]); !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("short message ID index error = %v, want ErrCorruptValue", err)
	}

	globalValue := encodeGlobalMessageIDIndexValue("room:1", 42)
	if gotKey, gotSeq, err := decodeGlobalMessageIDIndexValue(globalValue); err != nil || gotKey != "room:1" || gotSeq != 42 {
		t.Fatalf("global index round trip = (%q, %d, %v)", gotKey, gotSeq, err)
	}
	if _, _, err := decodeGlobalMessageIDIndexValue(encodeGlobalMessageIDIndexValue("", 42)); !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("empty channel global index error = %v, want ErrCorruptValue", err)
	}
	if _, _, err := decodeGlobalMessageIDIndexValue(encodeGlobalMessageIDIndexValue("room:1", 0)); !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("zero sequence global index error = %v, want ErrCorruptValue", err)
	}

	channelKey := ChannelKey("room:1")
	id := ChannelID{ID: "room", Type: 1}
	cache := newAppendKeyCache(channelKey, id)
	if !cache.initialized() {
		t.Fatal("append key cache is not initialized")
	}
	if got := cache.messageRowKey(42, messageHeaderFamilyID); !bytes.Equal(got, encodeMessageRowKey(channelKey, 42, messageHeaderFamilyID)) {
		t.Fatal("cached message row key differs from canonical encoding")
	}
	if got := cache.clientMsgNoIndexKey("client", 42); !bytes.Equal(got, encodeMessageClientMsgNoIndexKey(channelKey, "client", 42)) {
		t.Fatal("cached client-message key differs from canonical encoding")
	}
	if got := cache.idempotencyIndexKey("user", "client"); !bytes.Equal(got, encodeMessageIdempotencyIndexKey(channelKey, "user", "client")) {
		t.Fatal("cached idempotency key differs from canonical encoding")
	}
	senderKey := make([]byte, cache.senderSeqIndexKeyLen("user"))
	cache.writeSenderSeqIndexKey(senderKey, "user", 42)
	if !bytes.Equal(senderKey, encodeMessageSenderSeqIndexKey(channelKey, "user", 42)) {
		t.Fatal("cached sender-sequence key differs from canonical encoding")
	}
	if !bytes.Equal(cache.catalogKey, encodeCatalogKey(channelKey)) || !bytes.Equal(cache.catalogValue, encodeCatalogValue(id)) {
		t.Fatal("cached catalog data differs from canonical encoding")
	}
}

func TestRecordNormalizationPreservesCallerDataAndFillsDerivedFields(t *testing.T) {
	payload := []byte("payload")
	log := &ChannelLog{channelEntry: &channelEntry{id: ChannelID{ID: "room", Type: 3}}}
	row := log.recordToRow(42, Record{ID: 99, ClientMsgNo: "client", FromUID: "user", Payload: payload}, 1234)
	row = normalizeMessageRow(row)
	if row.MessageSeq != 42 || row.MessageID != 99 || row.ChannelID != "room" || row.ChannelType != 3 || row.ServerTimestampMS != 1234 {
		t.Fatalf("record conversion lost identity: %+v", row)
	}
	if row.PayloadSize != uint64(len(payload)) || row.PayloadHash != hashPayload(payload) {
		t.Fatalf("record conversion derived fields = size %d hash %d", row.PayloadSize, row.PayloadHash)
	}

	explicit := log.recordToRow(43, Record{ID: 100, Payload: payload, SizeBytes: 500, ServerTimestampMS: 999}, 1234)
	explicit = normalizeMessageRow(explicit)
	if explicit.PayloadSize != 500 || explicit.ServerTimestampMS != 999 {
		t.Fatalf("caller-provided fields overwritten: %+v", explicit)
	}
	if err := (messageRow{}).validate(); !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("zero message ID error = %v, want ErrInvalidArgument", err)
	}
	if !reflect.DeepEqual(row.Payload, payload) {
		t.Fatal("payload changed during normalization")
	}
}
