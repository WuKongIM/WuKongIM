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
	for key := range fields {
		if strings.EqualFold(key, "p95") {
			if _, err := strictLatencyGateFields(encoded, "", []string{"p95", "p999"}); err != nil {
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
	*l = LatencyLimit(value)
	return nil
}
