package rpctrace_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

func cohortJSONMap(t *testing.T, sample rpctrace.Sample) map[string]json.RawMessage {
	t.Helper()
	b, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestCommitCohortJSONLegacySampleDoesNotInventObservation(t *testing.T) {
	var sample rpctrace.Sample
	if err := json.Unmarshal([]byte(`{"trace_id":"legacy","side":"server","service_id":21,"spans":[]}`), &sample); err != nil {
		t.Fatal(err)
	}
	if raw, ok := cohortJSONMap(t, sample.Clone())["commit_cohorts"]; ok && string(raw) != "null" {
		t.Fatalf("legacy sample invented cohort observation: %s", raw)
	}
}

func TestCommitCohortJSONCloneDetachesWholeLinksAndReference(t *testing.T) {
	const input = `{"trace_id":"selected","side":"server","service_id":21,"spans":[],"commit_cohorts":{"links":[{"cohort_ref":{"instance":"instance-A","attempt":7},"request_ordinal":0},{"cohort_ref":{"instance":"instance-A","attempt":8},"request_ordinal":1,"rebuild_of":{"instance":"discarded-instance","attempt":6}}],"dropped":3}}`
	var sample rpctrace.Sample
	if err := json.Unmarshal([]byte(input), &sample); err != nil {
		t.Fatal(err)
	}
	cloned := sample.Clone()
	before := cohortJSONMap(t, cloned)["commit_cohorts"]
	if len(before) == 0 || string(before) == "null" {
		t.Fatal("typed JSON cohort sidecar was dropped")
	}
	var coverage struct {
		Links []struct {
			Ref struct {
				Instance string `json:"instance"`
				Attempt  uint64 `json:"attempt"`
			} `json:"cohort_ref"`
			Ordinal uint32 `json:"request_ordinal"`
		} `json:"links"`
		Dropped uint64 `json:"dropped"`
	}
	if err := json.Unmarshal(before, &coverage); err != nil {
		t.Fatal(err)
	}
	if len(coverage.Links) != 2 || coverage.Dropped != 3 || coverage.Links[0].Ref.Instance != "instance-A" || coverage.Links[0].Ref.Attempt != 7 || coverage.Links[1].Ordinal != 1 {
		t.Fatalf("whole-link contract=%+v", coverage)
	}
	// Mutate through the original typed sample, rather than replacing its JSON.
	// JSON tags identify the public field while avoiding a dependency on its Go name.
	root := reflect.ValueOf(&sample).Elem()
	var sidecar reflect.Value
	for i := 0; i < root.NumField(); i++ {
		if root.Type().Field(i).Tag.Get("json") == "commit_cohorts,omitempty" {
			sidecar = root.Field(i)
			break
		}
	}
	if !sidecar.IsValid() || sidecar.Kind() != reflect.Pointer || sidecar.IsNil() {
		t.Fatal("missing optional typed cohort field")
	}
	links := sidecar.Elem().FieldByName("Links")
	if !links.IsValid() || links.Kind() != reflect.Slice || links.Len() != 2 {
		t.Fatal("missing typed whole-link list")
	}
	ref := links.Index(0).FieldByName("CohortRef")
	instance := ref.FieldByName("Instance")
	if !instance.IsValid() || instance.Kind() != reflect.String || !instance.CanSet() {
		t.Fatal("missing typed reference instance")
	}
	instance.SetString("mutated-original")
	predecessor := links.Index(1).FieldByName("RebuildOf")
	if !predecessor.IsValid() || predecessor.Kind() != reflect.Pointer || predecessor.IsNil() {
		t.Fatal("missing typed discarded-build predecessor")
	}
	predecessor.Elem().FieldByName("Instance").SetString("mutated-predecessor")
	after := cohortJSONMap(t, cloned)["commit_cohorts"]
	if string(before) != string(after) {
		t.Fatal("Sample.Clone shared mutable cohort/reference storage")
	}
	original := cohortJSONMap(t, sample)["commit_cohorts"]
	if string(original) == string(after) {
		t.Fatal("fixture did not mutate the original typed sidecar")
	}
}
