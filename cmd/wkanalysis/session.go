package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

var errInvalidAnalysisSession = errors.New("cmd/wkanalysis: invalid analysis session")

type analysisSessionStore struct {
	mu           sync.Mutex
	runExpiresAt time.Time
	now          func() time.Time
	random       io.Reader
	tokens       map[[sha256.Size]byte]time.Time
}

func newAnalysisSessionStore(runExpiresAt time.Time, now func() time.Time) *analysisSessionStore {
	if now == nil {
		now = time.Now
	}
	return &analysisSessionStore{runExpiresAt: runExpiresAt, now: now, random: rand.Reader, tokens: make(map[[sha256.Size]byte]time.Time)}
}

func (s *analysisSessionStore) Issue() (string, time.Time, error) {
	now := s.now().UTC()
	expiresAt := now.Add(45 * time.Minute)
	if leaseBound := s.runExpiresAt.Add(-5 * time.Minute); leaseBound.Before(expiresAt) {
		expiresAt = leaseBound
	}
	if !expiresAt.After(now) || s.runExpiresAt.Sub(now) < 30*time.Minute {
		return "", time.Time{}, errInvalidAnalysisSession
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(s.random, raw); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, expiry := range s.tokens {
		if !expiry.After(now) {
			delete(s.tokens, key)
		}
	}
	s.tokens[digest] = expiresAt
	return token, expiresAt, nil
}

func (s *analysisSessionStore) Verify(_ context.Context, token string, _ *http.Request) (time.Time, error) {
	if len(token) < 32 {
		return time.Time{}, errInvalidAnalysisSession
	}
	digest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	expiresAt, ok := s.tokens[digest]
	if !ok || !expiresAt.After(now) {
		delete(s.tokens, digest)
		return time.Time{}, errInvalidAnalysisSession
	}
	return expiresAt, nil
}
