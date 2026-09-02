package reviewagentverify_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

func TestFileLedgerFiltersGenerationsWithoutReplacingAppendOnlyHistory(
	t *testing.T,
) {
	t.Parallel()

	workspace := t.TempDir()
	ledgerPath := filepath.Join(t.TempDir(), "evidence", "ledger.jsonl")
	ledger, err := verify.NewFileLedger(ledgerPath, workspace)
	require.NoError(t, err)

	records, err := ledger.List(testGeneration())
	require.NoError(t, err)
	require.Empty(t, records)

	otherGeneration := testGeneration()
	otherGeneration.HeadSHA = strings.Repeat("9", 40)
	first := trustedCheckEvidence("go-unit", contract.CheckOutcomeFailed, 1, "first")
	other := trustedCheckEvidence("go-unit", contract.CheckOutcomePassed, 0, "other generation")
	latest := trustedCheckEvidence("go-vet", contract.CheckOutcomePassed, 0, "latest")
	require.NoError(t, ledger.Append(testGeneration(), first))
	require.NoError(t, ledger.Append(otherGeneration, other))
	require.NoError(t, ledger.Append(testGeneration(), latest))

	records, err = ledger.List(testGeneration())
	require.NoError(t, err)
	require.Len(t, records, 2)
	require.Equal(t, uint64(1), records[0].Sequence)
	require.Equal(t, first, records[0].Evidence)
	require.Equal(t, uint64(3), records[1].Sequence)
	require.Equal(t, latest, records[1].Evidence)
	require.NotEmpty(t, records[1].PreviousDigest)

	var unavailable *verify.FileLedger
	require.EqualError(
		t,
		unavailable.Append(testGeneration(), first),
		"evidence ledger is unavailable",
	)
	_, err = unavailable.List(testGeneration())
	require.EqualError(t, err, "evidence ledger is unavailable")
}

func TestFileLedgerRejectsSymlinkPathsBackIntoCandidateWorkspace(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	external := t.TempDir()
	parentLink := filepath.Join(external, "workspace-link")
	require.NoError(t, os.Symlink(workspace, parentLink))
	_, err := verify.NewFileLedger(
		filepath.Join(parentLink, "ledger.jsonl"),
		workspace,
	)
	require.EqualError(t, err, "evidence ledger symlink escapes are unsafe")

	target := filepath.Join(external, "target.jsonl")
	require.NoError(t, os.WriteFile(target, []byte(""), 0o600))
	ledgerLink := filepath.Join(external, "ledger-link.jsonl")
	require.NoError(t, os.Symlink(target, ledgerLink))
	_, err = verify.NewFileLedger(ledgerLink, workspace)
	require.EqualError(t, err, "evidence ledger path is a symlink")
}

func TestFileLedgerRejectsMalformedOrTamperedRecords(t *testing.T) {
	t.Parallel()

	validRecord := verify.LedgerRecord{
		Sequence:   1,
		Generation: testGeneration(),
		Evidence: trustedCheckEvidence(
			"go-unit",
			contract.CheckOutcomePassed,
			0,
			"ok",
		),
		CreatedAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	validBody, err := json.Marshal(validRecord)
	require.NoError(t, err)

	discontinuousSequence := validRecord
	discontinuousSequence.Sequence = 2
	discontinuousSequence.PreviousDigest = digest("9")
	discontinuousBody, err := json.Marshal(discontinuousSequence)
	require.NoError(t, err)

	invalidPredecessor := validRecord
	invalidPredecessor.PreviousDigest = digest("8")
	invalidPredecessorBody, err := json.Marshal(invalidPredecessor)
	require.NoError(t, err)

	tests := []struct {
		name string
		body []byte
	}{
		{name: "invalid JSON", body: []byte("{")},
		{name: "unknown fields", body: []byte(`{"unknown":true}`)},
		{name: "trailing JSON", body: append(append([]byte(nil), validBody...), []byte(" {}")...)},
		{name: "discontinuous first sequence", body: discontinuousBody},
		{name: "invalid first predecessor", body: invalidPredecessorBody},
		{name: "oversized record", body: []byte(strings.Repeat("x", (1<<20)+1))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			workspace := t.TempDir()
			ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
			ledger, err := verify.NewFileLedger(ledgerPath, workspace)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(ledgerPath, append(test.body, '\n'), 0o600))
			_, err = ledger.List(testGeneration())
			require.Error(t, err)
		})
	}
}

func TestFileLedgerDetectsHashChainMutationAndRejectsInvalidAppends(
	t *testing.T,
) {
	t.Parallel()

	workspace := t.TempDir()
	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	ledger, err := verify.NewFileLedger(ledgerPath, workspace)
	require.NoError(t, err)
	evidence := trustedCheckEvidence("go-unit", contract.CheckOutcomePassed, 0, "ok")
	require.NoError(t, ledger.Append(testGeneration(), evidence))
	require.NoError(t, ledger.Append(testGeneration(), evidence))
	body, err := os.ReadFile(ledgerPath)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	require.Len(t, lines, 2)
	var second verify.LedgerRecord
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &second))
	second.PreviousDigest = digest("7")
	tamperedSecond, err := json.Marshal(second)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		ledgerPath,
		[]byte(lines[0]+"\n"+string(tamperedSecond)+"\n"),
		0o600,
	))
	_, err = ledger.List(testGeneration())
	require.EqualError(t, err, "evidence ledger chain is discontinuous")

	fresh, err := verify.NewFileLedger(
		filepath.Join(t.TempDir(), "ledger.jsonl"),
		workspace,
	)
	require.NoError(t, err)
	invalidGeneration := testGeneration()
	invalidGeneration.HeadSHA = "short"
	require.Error(t, fresh.Append(invalidGeneration, evidence))
	invalidEvidence := evidence
	invalidEvidence.DurationMS = 0
	require.Error(t, fresh.Append(testGeneration(), invalidEvidence))
}
