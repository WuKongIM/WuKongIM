package chatlifecycle

import (
	"bytes"
	"encoding/json"
	"strings"
)

// UnmarshalJSON gives the new P95 threshold a closed wire shape. Historical
// P99 objects keep their original decoding semantics and serialized bytes.
func (l *LatencyLimit) UnmarshalJSON(encoded []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return err
	}
	p95, secondary := false, false
	for key := range fields {
		if strings.EqualFold(key, "p95") {
			p95 = true
			names := []string{"p95"}
			if _, secondary = fields["p999"]; secondary {
				names = append(names, "p999")
			}
			if _, err := strictLatencyGateFields(encoded, "", names); err != nil {
				return err
			}
			break
		}
	}
	type plain LatencyLimit
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if p95 && (value.P95 <= 0 || (secondary && value.P999 <= 0)) {
		return ErrReportInvalid
	}
	*l = LatencyLimit(value)
	return nil
}
