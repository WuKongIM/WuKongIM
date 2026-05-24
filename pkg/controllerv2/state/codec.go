package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"time"
)

type checksumClusterState struct {
	SchemaVersion    uint32            `json:"schema_version"`
	ClusterID        string            `json:"cluster_id"`
	Revision         uint64            `json:"revision"`
	AppliedRaftIndex uint64            `json:"applied_raft_index"`
	UpdatedAt        time.Time         `json:"updated_at"`
	Config           ClusterConfig     `json:"config"`
	Controllers      []ControllerVoter `json:"controllers"`
	Nodes            []Node            `json:"nodes"`
	Slots            []SlotAssignment  `json:"slots"`
	HashSlots        HashSlotTable     `json:"hash_slots"`
	Tasks            []ReconcileTask   `json:"tasks"`
}

// Encode returns normalized canonical JSON with a CRC32C checksum.
func Encode(st ClusterState) ([]byte, error) {
	st = st.Clone()
	st.Normalize()
	if err := st.Validate(); err != nil {
		return nil, err
	}
	checksum, err := Checksum(st)
	if err != nil {
		return nil, err
	}
	st.Checksum = checksum
	return json.Marshal(st)
}

// Decode parses, verifies, normalizes, and validates a cluster-state JSON payload.
func Decode(data []byte) (ClusterState, error) {
	var st ClusterState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&st); err != nil {
		return ClusterState{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return ClusterState{}, invalid("trailing JSON token")
		}
		return ClusterState{}, err
	}
	if st.SchemaVersion != CurrentSchemaVersion {
		return ClusterState{}, fmt.Errorf("%w: %d", ErrUnsupportedSchema, st.SchemaVersion)
	}
	actual := st.Checksum
	if actual == "" {
		return ClusterState{}, fmt.Errorf("%w: missing checksum", ErrChecksumMismatch)
	}
	expected, err := Checksum(st)
	if err != nil {
		return ClusterState{}, err
	}
	if actual != expected {
		return ClusterState{}, fmt.Errorf("%w: expected %s got %s", ErrChecksumMismatch, expected, actual)
	}
	st.Normalize()
	st.Checksum = expected
	if err := st.Validate(); err != nil {
		return ClusterState{}, err
	}
	st.Checksum = expected
	return st, nil
}

// Checksum returns the CRC32C Castagnoli checksum over canonical JSON without checksum.
func Checksum(st ClusterState) (string, error) {
	st = st.Clone()
	st.Checksum = ""
	st.Normalize()
	payload, err := json.Marshal(checksumView(st))
	if err != nil {
		return "", err
	}
	table := crc32.MakeTable(crc32.Castagnoli)
	return fmt.Sprintf("crc32c:%08x", crc32.Checksum(payload, table)), nil
}

func checksumView(st ClusterState) checksumClusterState {
	return checksumClusterState{
		SchemaVersion:    st.SchemaVersion,
		ClusterID:        st.ClusterID,
		Revision:         st.Revision,
		AppliedRaftIndex: st.AppliedRaftIndex,
		UpdatedAt:        st.UpdatedAt,
		Config:           st.Config,
		Controllers:      st.Controllers,
		Nodes:            st.Nodes,
		Slots:            st.Slots,
		HashSlots:        st.HashSlots,
		Tasks:            st.Tasks,
	}
}
