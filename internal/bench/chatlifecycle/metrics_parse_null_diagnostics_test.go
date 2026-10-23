package chatlifecycle

import "testing"

func TestMetricsParseReasonsRejectsNullElements(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
	}{
		{"parse_position", `,"parse_reasons":[null,"",""]`},
		{"successful_position", `,"parse_reasons":["invalid_value",null,""]`},
		{"transport_position", `,"parse_reasons":["invalid_value","",null]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := parseDiagnosticDecode(t, parseDiagnosticJSON(tc.field))
			if failure.MetricsFetch == nil || failure.MetricsFetch.SourceMask != 5 || failure.MetricsFetch.Kinds[0] != "parse" || failure.MetricsFetch.Kinds[2] != "transport" {
				t.Fatal("optional element type changed old diagnostic fields")
			}
			if failure.MetricsFetch.ParseReasons != nil {
				t.Fatal("null element must discard the optional vector")
			}
		})
	}
}
