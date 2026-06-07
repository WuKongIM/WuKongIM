package message

import (
	"context"
	"errors"
	"testing"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestSyncChannelMessagesNormalizesPersonChannelAndCapsLimit(t *testing.T) {
	reader := &recordingChannelMessageReader{
		page: ChannelMessagePage{
			Messages: []SyncedMessage{{MessageID: 88, MessageSeq: 9}},
			HasMore:  true,
		},
	}
	app := New(Options{MessageReader: reader})

	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:        "u1",
		ChannelID:       "u2",
		ChannelType:     channelTypePerson,
		StartMessageSeq: 1,
		EndMessageSeq:   10,
		Limit:           20000,
		PullMode:        PullModeUp,
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if !result.More {
		t.Fatalf("More = false, want true")
	}
	if len(result.Messages) != 1 || result.Messages[0].MessageID != 88 {
		t.Fatalf("messages = %#v, want message 88", result.Messages)
	}
	if len(reader.queries) != 1 {
		t.Fatalf("queries = %#v, want one query", reader.queries)
	}
	wantChannel := ChannelID{ID: runtimechannelid.EncodePersonChannel("u1", "u2"), Type: channelTypePerson}
	if got := reader.queries[0]; got.ChannelID != wantChannel || got.StartSeq != 1 || got.EndSeq != 10 || got.Limit != 10000 || got.PullMode != PullModeUp {
		t.Fatalf("query = %#v, want normalized person capped-limit query", got)
	}
}

func TestSyncChannelMessagesReturnsEmptyForMissingChannelRuntime(t *testing.T) {
	app := New(Options{MessageReader: &recordingChannelMessageReader{err: metadb.ErrNotFound}})

	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:    "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		Limit:       30,
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if result.More || len(result.Messages) != 0 {
		t.Fatalf("result = %#v, want empty page", result)
	}
}

func TestSyncChannelMessagesRejectsMissingRequiredFields(t *testing.T) {
	app := New(Options{MessageReader: &recordingChannelMessageReader{}})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		ChannelID:   "g1",
		ChannelType: 2,
	})

	if !errors.Is(err, ErrSyncLoginUIDRequired) {
		t.Fatalf("error = %v, want login uid required", err)
	}
}

func TestSyncChannelMessagesPropagatesReaderError(t *testing.T) {
	readerErr := errors.New("reader failed")
	app := New(Options{MessageReader: &recordingChannelMessageReader{err: readerErr}})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:    "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		Limit:       30,
	})

	if !errors.Is(err, readerErr) {
		t.Fatalf("error = %v, want reader error", err)
	}
}

type recordingChannelMessageReader struct {
	queries []ChannelMessageQuery
	page    ChannelMessagePage
	err     error
}

func (r *recordingChannelMessageReader) SyncMessages(_ context.Context, query ChannelMessageQuery) (ChannelMessagePage, error) {
	r.queries = append(r.queries, query)
	return r.page, r.err
}
