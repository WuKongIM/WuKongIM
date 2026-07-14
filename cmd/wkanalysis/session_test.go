package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAnalysisSessionStoreIssuesNonRenewableLeaseBoundToken(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	store := newAnalysisSessionStore(now.Add(40*time.Minute), func() time.Time { return now })
	store.random = strings.NewReader(strings.Repeat("r", 32))
	token, expiresAt, err := store.Issue()
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if expiresAt != now.Add(35*time.Minute) {
		t.Fatalf("expires = %v, want lease minus five minutes", expiresAt)
	}
	if verified, err := store.Verify(context.Background(), token, nil); err != nil || verified != expiresAt {
		t.Fatalf("Verify() = %v, %v", verified, err)
	}
	now = expiresAt
	if _, err := store.Verify(context.Background(), token, nil); !errors.Is(err, errInvalidAnalysisSession) {
		t.Fatalf("Verify(expired) error = %v", err)
	}
}
