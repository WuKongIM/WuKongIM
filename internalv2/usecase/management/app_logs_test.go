package management

import (
	"context"
	"errors"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestApplicationLogEntriesValidatesNodeAndLimit(t *testing.T) {
	app := New(Options{ApplicationLogs: &fakeApplicationLogReader{}})

	tests := []ApplicationLogEntriesRequest{
		{NodeID: 0, Source: "app", Limit: 10},
		{NodeID: 1, Source: "app", Limit: -1},
	}
	for _, req := range tests {
		if _, err := app.ApplicationLogEntries(context.Background(), req); !errors.Is(err, metadb.ErrInvalidArgument) {
			t.Fatalf("ApplicationLogEntries(%#v) error = %v, want %v", req, err, metadb.ErrInvalidArgument)
		}
	}
}

func TestApplicationLogEntriesRequiresReader(t *testing.T) {
	app := New(Options{})

	_, err := app.ApplicationLogEntries(context.Background(), ApplicationLogEntriesRequest{NodeID: 1})
	if !errors.Is(err, ErrApplicationLogReaderUnavailable) {
		t.Fatalf("ApplicationLogEntries() error = %v, want %v", err, ErrApplicationLogReaderUnavailable)
	}
}

func TestApplicationLogSourcesDelegates(t *testing.T) {
	modifiedAt := time.Unix(1700000000, 0).UTC()
	reader := &fakeApplicationLogReader{
		sourcesResp: ApplicationLogSourcesResponse{
			NodeID: 2,
			Sources: []ApplicationLogSource{{
				Name:       "app",
				File:       "wukongim.log",
				Available:  true,
				SizeBytes:  1234,
				ModifiedAt: modifiedAt,
			}},
		},
	}
	app := New(Options{ApplicationLogs: reader})

	got, err := app.ApplicationLogSources(context.Background(), ApplicationLogSourcesRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("ApplicationLogSources() error = %v", err)
	}
	if reader.sourcesReq.NodeID != 2 {
		t.Fatalf("sources request = %#v, want node 2", reader.sourcesReq)
	}
	if got.NodeID != 2 || len(got.Sources) != 1 || got.Sources[0].Name != "app" || !got.Sources[0].ModifiedAt.Equal(modifiedAt) {
		t.Fatalf("sources response = %#v, want delegated response", got)
	}
}

func TestApplicationLogEntriesDelegatesRequestFields(t *testing.T) {
	entryTime := time.Unix(1700000100, 0).UTC()
	reader := &fakeApplicationLogReader{
		entriesResp: ApplicationLogEntriesResponse{
			NodeID:  3,
			Source:  "error",
			Cursor:  "next",
			Rotated: true,
			Items: []ApplicationLogEntry{{
				Seq:       99,
				Offset:    2048,
				Time:      entryTime,
				Level:     "ERROR",
				Module:    "gateway",
				Caller:    "handler.go:10",
				Message:   "send failed",
				Fields:    map[string]any{"uid": "u1"},
				Raw:       `{"level":"ERROR","msg":"send failed"}`,
				Truncated: true,
			}},
		},
	}
	app := New(Options{ApplicationLogs: reader})

	got, err := app.ApplicationLogEntries(context.Background(), ApplicationLogEntriesRequest{
		NodeID:  3,
		Source:  "error",
		Limit:   50,
		Cursor:  "cursor-1",
		Keyword: "send",
		Levels:  []string{"ERROR", "WARN"},
	})
	if err != nil {
		t.Fatalf("ApplicationLogEntries() error = %v", err)
	}
	if reader.entriesReq.NodeID != 3 ||
		reader.entriesReq.Source != "error" ||
		reader.entriesReq.Limit != 50 ||
		reader.entriesReq.Cursor != "cursor-1" ||
		reader.entriesReq.Keyword != "send" ||
		len(reader.entriesReq.Levels) != 2 ||
		reader.entriesReq.Levels[0] != "ERROR" ||
		reader.entriesReq.Levels[1] != "WARN" {
		t.Fatalf("entries request = %#v, want delegated fields", reader.entriesReq)
	}
	if got.NodeID != 3 || got.Source != "error" || got.Cursor != "next" || !got.Rotated || len(got.Items) != 1 {
		t.Fatalf("entries response = %#v, want delegated response", got)
	}
}

type fakeApplicationLogReader struct {
	sourcesReq  ApplicationLogSourcesRequest
	entriesReq  ApplicationLogEntriesRequest
	sourcesResp ApplicationLogSourcesResponse
	entriesResp ApplicationLogEntriesResponse
	err         error
}

func (f *fakeApplicationLogReader) ApplicationLogSources(ctx context.Context, req ApplicationLogSourcesRequest) (ApplicationLogSourcesResponse, error) {
	f.sourcesReq = req
	return f.sourcesResp, f.err
}

func (f *fakeApplicationLogReader) ApplicationLogEntries(ctx context.Context, req ApplicationLogEntriesRequest) (ApplicationLogEntriesResponse, error) {
	f.entriesReq = req
	return f.entriesResp, f.err
}
