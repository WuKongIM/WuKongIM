package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithIO([]string{"missing"}, nil, &stderr)
	if code == 0 {
		t.Fatal("exit code = 0, want failure")
	}
	if stderr.Len() == 0 {
		t.Fatal("stderr is empty")
	}
}

func TestRunRejectsUnknownFormatBeforeOpeningStore(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithIO([]string{"--format", "yaml", "query", "show tables"}, nil, &stderr)
	if code != exitConfig {
		t.Fatalf("exit code = %d, want %d", code, exitConfig)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("unknown format")) {
		t.Fatalf("stderr = %q, want format error", stderr.String())
	}
}

func TestRunQueryShowTables(t *testing.T) {
	metaPath := t.TempDir()
	metaDB, err := meta.Open(metaPath)
	if err != nil {
		t.Fatalf("meta.Open(): %v", err)
	}
	if err := metaDB.Close(); err != nil {
		t.Fatalf("meta Close(): %v", err)
	}
	store, err := inspect.OpenStore(inspect.Options{MetaPath: metaPath})
	if err != nil {
		t.Fatalf("OpenStore(): %v", err)
	}
	defer store.Close()

	var stdout, stderr bytes.Buffer
	code := runQuery(context.Background(), store, "table", "show tables", &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("meta.user")) {
		t.Fatalf("stdout = %q, want meta.user", stdout.String())
	}
}

func TestRunREPLReturnsFailureAfterQueryError(t *testing.T) {
	metaPath := t.TempDir()
	metaDB, err := meta.Open(metaPath)
	if err != nil {
		t.Fatalf("meta.Open(): %v", err)
	}
	if err := metaDB.Close(); err != nil {
		t.Fatalf("meta Close(): %v", err)
	}
	store, err := inspect.OpenStore(inspect.Options{MetaPath: metaPath})
	if err != nil {
		t.Fatalf("OpenStore(): %v", err)
	}
	defer store.Close()

	var stdout, stderr bytes.Buffer
	code := runREPL(context.Background(), store, "table", bytes.NewBufferString("select * from meta.user limit 1\n"), &stdout, &stderr)
	if code != exitQuery {
		t.Fatalf("exit code = %d, want %d", code, exitQuery)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("query error")) {
		t.Fatalf("stderr = %q, want query error", stderr.String())
	}
}
