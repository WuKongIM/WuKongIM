package chatlifecycle

import (
	"bytes"
	"encoding/json"
	"strings"
)

// validPairedReportSync binds persisted cumulative P95 counts to their actual
// successful-sync histogram; historical report contracts remain unchanged.
func validPairedReportSync(r Report) bool {
	if r.SchemaVersion != localPairedP95ReportSchemaVersion {
		return true
	}
	c, limits := r.Sync.Thresholds, r.Thresholds.Latency.Sync
	return c.P95Limit == limits.P95 && c.P99Limit == 0 && c.P999Limit == limits.P999 &&
		validWorkerSyncThresholds(r.Sync)
}

// validPairedReportWire checks canonical new counter keys before JSON decoding
// can normalize nulls, duplicates or aliases. An omitted AboveP95 still means0.
func validPairedReportWire(encoded []byte, r Report) bool {
	if r.SchemaVersion != localPairedP95ReportSchemaVersion {
		return true
	}
	sync, err := pairedReportChild(encoded, "sync")
	if err != nil {
		return false
	}
	counters, err := pairedReportChild(sync, "thresholds")
	if err != nil {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(counters, &fields) != nil {
		return false
	}
	names := []string{"P95Limit", "P99Limit", "P999Limit", "Count", "AboveP99", "AboveP999", "Above10Seconds"}
	if _, ok := fields["AboveP95"]; ok {
		names = append(names, "AboveP95")
	}
	_, err = strictLatencyGateFields(counters, "", names)
	return err == nil
}

// pairedReportChild preserves one exact child without changing other existing
// report fields; the ordinary typed decoder still validates the whole object.
func pairedReportChild(encoded []byte, name string) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(encoded))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, ErrReportInvalid
	}
	var child json.RawMessage
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, ErrReportInvalid
		}
		key, ok := token.(string)
		if !ok {
			return nil, ErrReportInvalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, ErrReportInvalid
		}
		if strings.EqualFold(key, name) {
			if key != name || child != nil {
				return nil, ErrReportInvalid
			}
			child = value
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || child == nil || latencyGateJSONEnd(d) != nil {
		return nil, ErrReportInvalid
	}
	return child, nil
}
