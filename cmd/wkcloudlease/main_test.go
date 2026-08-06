package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestDryRunExercisesCompleteFakeLifecycleWithoutBackgroundWork(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(&stdout)
	command.SetArgs([]string{"dry-run"})

	if err := command.Execute(); err != nil {
		t.Fatalf("dry-run error = %v", err)
	}
	var result dryRunResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode dry-run output: %v\n%s", err, stdout.String())
	}
	if result.Schema != dryRunSchemaV1 || result.Provider != "fake" {
		t.Fatalf("dry-run identity = %#v", result)
	}
	if result.FinalState != "released" || result.ResidualResources != 0 {
		t.Fatalf("dry-run final result = %#v, want released zero inventory", result)
	}
	wantOperations := []string{"quote", "acquire", "inspect", "grant_access", "revoke_access", "release", "sweep"}
	if len(result.Operations) != len(wantOperations) {
		t.Fatalf("dry-run operations = %v, want %v", result.Operations, wantOperations)
	}
	for index, want := range wantOperations {
		if result.Operations[index] != want {
			t.Fatalf("dry-run operation[%d] = %q, want %q", index, result.Operations[index], want)
		}
	}
	if result.SweepExamined != 0 {
		t.Fatalf("dry-run sweep examined = %d, want 0 after zero-inventory proof", result.SweepExamined)
	}
}
