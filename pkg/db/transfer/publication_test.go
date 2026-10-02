package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func publicationFixture(t *testing.T) []byte {
	t.Helper()
	b, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	require.NoError(t, err)
	return b
}

func seedPublicationStore(t *testing.T, metadata []byte) db.NodeStoreOptions {
	t.Helper()
	source, opts := seedVerifyNodeStore(t, 256, verifySeedOptions{})
	log, err := source.Messages().Channel("g1:2", msgdb.ChannelID{ID: "g1", Type: 2})
	require.NoError(t, err)
	_, err = log.Append(context.Background(), []msgdb.Record{{ID: 1003, ClientMsgNo: "c3", FromUID: "u1", Payload: []byte("publication"), PublicationMetadata: metadata, ServerTimestampMS: 4000}}, msgdb.AppendOptions{})
	require.NoError(t, err)
	require.NoError(t, log.Close())
	closeVerifyNodeStore(t, source)
	return opts
}

func TestPublicationOfflineRoundTripAndOwnedInspection(t *testing.T) {
	ctx := context.Background()
	for _, will := range []bool{false, true} {
		metadata := publicationFixture(t)
		if will {
			m, err := publication.Decode(metadata)
			require.NoError(t, err)
			m.Source, m.AcceptedAtMS = publication.SourceWill, 0
			metadata, err = publication.Encode(m)
			require.NoError(t, err)
		}
		opts := seedPublicationStore(t, metadata)
		original := openVerifyInspectStore(t, opts, 256)
		root := filepath.Join(t.TempDir(), "bundle")
		_, err := ExportBundle(ctx, root, original, ExportOptions{HashSlotCount: 256, PageSize: 1, MessageFileRows: 1})
		require.NoError(t, err)
		manifest, err := LoadManifest(root)
		require.NoError(t, err)
		found := 0
		for _, entry := range manifest.Files {
			if entry.Kind != FileKindMessageMessages {
				continue
			}
			line, err := os.ReadFile(filepath.Join(root, entry.Path))
			require.NoError(t, err)
			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(line, &raw))
			if bytes.Equal(raw["message_id"], []byte("1003")) {
				found++
				var encoded string
				require.NoError(t, json.Unmarshal(raw["publication_metadata_b64"], &encoded))
				require.Equal(t, base64.StdEncoding.EncodeToString(metadata), encoded)
			} else {
				require.NotContains(t, raw, "publication_metadata_b64", "native row layout changed")
			}
		}
		require.Equal(t, 1, found)
		_, err = ValidateBundle(ctx, root, ImportOptions{HashSlotCount: 256})
		require.NoError(t, err)
		target, targetOpts := openExportNodeStore(t, t.TempDir())
		_, err = ImportBundle(ctx, root, target, ImportOptions{HashSlotCount: 256, RequireEmpty: true, MessageBatchBytes: 32})
		require.NoError(t, err)
		rows := readImportMessages(t, ctx, target.Messages(), "g1:2", msgdb.ChannelID{ID: "g1", Type: 2}, 1, 3)
		require.Len(t, rows, 3)
		require.Equal(t, metadata, rows[2].PublicationMetadata)
		inspected, err := msgdb.InspectMessages(ctx, target.Messages(), msgdb.InspectMessageRequest{ChannelKey: "g1:2", AfterSeq: 2, Limit: 1})
		require.NoError(t, err)
		require.Len(t, inspected.Rows, 1)
		require.Equal(t, metadata, inspected.Rows[0]["publication_metadata"])
		clear(inspected.Rows[0]["publication_metadata"].([]byte))
		rows = readImportMessages(t, ctx, target.Messages(), "g1:2", msgdb.ChannelID{ID: "g1", Type: 2}, 3, 1)
		require.Equal(t, metadata, rows[0].PublicationMetadata)
		closeVerifyNodeStore(t, target)
		restored := openVerifyInspectStore(t, targetOpts, 256)
		for _, mode := range []VerifyMode{VerifyModeSummary, VerifyModeFull} {
			report, err := VerifyStores(ctx, original, restored, VerifyOptions{HashSlotCount: 256, PageSize: 1, Mode: mode})
			require.NoError(t, err)
			require.True(t, report.Equal, "%+v", report)
		}
	}
}

func TestPublicationOfflineVerificationDetectsMetadataOnlyChange(t *testing.T) {
	metadata := publicationFixture(t)
	original := openVerifyInspectStore(t, seedPublicationStore(t, metadata), 256)
	metadata[2] = 0
	changed := openVerifyInspectStore(t, seedPublicationStore(t, metadata), 256)
	for _, mode := range []VerifyMode{VerifyModeSummary, VerifyModeFull} {
		report, err := VerifyStores(context.Background(), original, changed, VerifyOptions{HashSlotCount: 256, Mode: mode})
		require.NoError(t, err)
		require.False(t, report.Equal)
		require.True(t, verifyReportHasMismatch(report, "message.messages"))
	}
}

func TestPublicationJSONLValidationAndNativeFixture(t *testing.T) {
	base := `{"channel_key":"g:2","message_seq":1,"message_id":1,"client_msg_no":"","from_uid":"","server_timestamp_ms":1000,"payload_b64":"eA=="}`
	for _, field := range []string{"", `,"publication_metadata_b64":""`, `,"publication_metadata_b64":"` + base64.StdEncoding.EncodeToString(publicationFixture(t)) + `"`} {
		_, err := decodeRecord(FileKindMessageMessages, []byte(base[:len(base)-1]+field+"}"))
		require.NoError(t, err)
	}
	wire, err := json.Marshal(MessageRecord{ChannelKey: "g:2", MessageSeq: 1, MessageID: 1, ServerTimestampMS: 1000, PayloadB64: "eA=="})
	require.NoError(t, err)
	require.Equal(t, base, string(wire))
	for _, bad := range []string{"!", "AQ==", "Ag==", base64.StdEncoding.EncodeToString(make([]byte, publication.MaxEncodedBytes+1))} {
		_, err := decodeRecord(FileKindMessageMessages, []byte(base[:len(base)-1]+`,"publication_metadata_b64":"`+bad+`"}`))
		require.Error(t, err)
	}
	m, err := publication.Decode(publicationFixture(t))
	require.NoError(t, err)
	m.AcceptedAtMS = int64(^uint64(0) >> 1)
	bad, err := publication.Encode(m)
	require.NoError(t, err)
	_, err = decodeRecord(FileKindMessageMessages, []byte(base[:len(base)-1]+`,"publication_metadata_b64":"`+base64.StdEncoding.EncodeToString(bad)+`"}`))
	require.Error(t, err, "overflowing expiry accepted")
	m.Source, m.AcceptedAtMS = publication.SourceWill, 0
	bad, err = publication.Encode(m)
	require.NoError(t, err)
	base = `{"channel_key":"g:2","message_seq":1,"message_id":1,"server_timestamp_ms":0,"payload_b64":"eA=="`
	_, err = decodeRecord(FileKindMessageMessages, []byte(base+`,"publication_metadata_b64":"`+base64.StdEncoding.EncodeToString(bad)+`"}`))
	require.Error(t, err, "Will without original clock accepted")
}

func TestPublicationImportByteBudgetIncludesMetadata(t *testing.T) {
	ctx := context.Background()
	store := openImportNodeStore(t)
	log, err := store.Messages().Channel("g:2", msgdb.ChannelID{ID: "g", Type: 2})
	require.NoError(t, err)
	defer log.Close()
	state := newMessageImportState(ctx, store.Messages(), nil, ImportOptions{MessageBatchSize: 10, MessageBatchBytes: 40}, &ImportStats{})
	state.currentKey, state.log = "g:2", log
	require.NoError(t, state.visit(MessageRecord{ChannelKey: "g:2", MessageID: 1, MessageSeq: 1, ClientMsgNo: "c", FromUID: "u", Payload: []byte("x"), ServerTimestampMS: 1000, PublicationMetadata: publicationFixture(t)}))
	rows, err := log.Read(ctx, 1, msgdb.ReadOptions{Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, rows, 1, "metadata should exhaust byte budget and flush before row limit")
	require.Equal(t, publicationFixture(t), rows[0].PublicationMetadata)
}

func TestPublicationJSONLRequiresOriginalSourceTimestamp(t *testing.T) {
	for _, source := range []publication.Source{publication.SourceMQTT, publication.SourceWill} {
		m, err := publication.Decode(publicationFixture(t))
		require.NoError(t, err)
		m.Source, m.Properties = source, nil
		if source == publication.SourceWill {
			m.AcceptedAtMS = 0
		}
		metadata, err := publication.Encode(m)
		require.NoError(t, err)
		for _, timestamp := range []int64{0, -1} {
			line, err := json.Marshal(MessageRecord{ChannelKey: "g:2", MessageSeq: 1, MessageID: 1, PayloadB64: "eA==", ServerTimestampMS: timestamp, PublicationMetadataB64: base64.StdEncoding.EncodeToString(metadata)})
			require.NoError(t, err)
			_, err = decodeRecord(FileKindMessageMessages, line)
			require.Error(t, err, "source=%d timestamp=%d", source, timestamp)
		}
	}
}

func TestPublicationNativeVerificationDigestUnchanged(t *testing.T) {
	row := msgdb.InspectMessageRow{"message_seq": uint64(1), "message_id": uint64(2), "client_msg_no": "c", "from_uid": "u", "server_timestamp_ms": int64(1000), "payload_hash": uint64(3), "payload_size": uint64(1), "payload": []byte("x")}
	base := `{"channel_key":"g:2","message_seq":1,"message_id":2,"client_msg_no":"c","from_uid":"u","server_timestamp_ms":1000,"payload_hash":3,"payload_size":1`
	for _, mode := range []VerifyMode{VerifyModeSummary, VerifyModeFull} {
		suffix := "}\n"
		if mode == VerifyModeFull {
			suffix = ",\"payload\":\"eA==\"}\n"
		}
		want := sha256.Sum256([]byte(base + suffix))
		digest := newVerifyDigest()
		require.NoError(t, digest.writeMessageRow("g:2", row, mode))
		require.Equal(t, hex.EncodeToString(want[:]), digest.sum())
	}
}
