package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"slices"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

// MQTTStorageRoutingKey is the stable authority for cluster storage escrow.
// One body-free Slot row allocates chunks, never individual publications.
const MQTTStorageRoutingKey = "wk:mqtt:shared-storage:v1"
const mqttStorageMaxNodes = 1024

// mqttStorageMaxLedgerBytes covers 1,024 grants/members with maximum-width
// uint64 JSON fields and the checksummed envelope; readers and writers agree.
const mqttStorageMaxLedgerBytes = 256 << 10

// MQTTStorageGrant is a non-expiring node escrow; reboot never resets its revision.
type MQTTStorageGrant struct {
	NodeID   uint64 `json:"node_id"`
	Revision uint64 `json:"revision"`
	Bytes    uint64 `json:"bytes"`
	// Debt records reconstructed startup responsibility, including unfunded legacy data.
	Debt uint64 `json:"debt"`
	// Initialized requires a canonical startup scan, never a zero-value assumption.
	Initialized bool `json:"initialized"`
}

type mqttStorageLedger struct {
	Key            string             `json:"key"`
	Limit          uint64             `json:"limit"`
	Grants         []MQTTStorageGrant `json:"grants"`
	Members        []uint64           `json:"members"`
	RosterRevision uint64             `json:"roster_revision"`
}

// MQTTStorageAdjustment changes one exact grant. Releases require the caller's
// durable local retirement; a timeout alone grants no release authority.
type MQTTStorageAdjustment struct {
	NodeID           uint64 `json:"node_id"`
	ExpectedRevision uint64 `json:"expected_revision"`
	ExpectedBytes    uint64 `json:"expected_bytes"`
	TargetBytes      uint64 `json:"target_bytes"`
	ClusterLimit     uint64 `json:"cluster_limit"`
	// Members is the sorted complete storage roster, including joining and leaving nodes.
	Members            []uint64 `json:"members"`
	MembershipRevision uint64   `json:"membership_revision"`
	// InitialDebt follows a canonical scan or a proved local decrease.
	InitialDebt *uint64 `json:"initial_debt,omitempty"`
}

// MQTTStorageResult is meaningful only after the authoritative Slot commit.
// Conflict carries current evidence for bounded reconciliation of unknown replies.
type MQTTStorageResult struct {
	Status string           `json:"status"`
	Grant  MQTTStorageGrant `json:"grant"`
	Limit  uint64           `json:"limit"`
	Total  uint64           `json:"total"`
	// Ready requires every roster member to have registered and funded its existing debt.
	Ready bool `json:"ready"`
	// MembersMatch prevents reuse of grants under a different storage roster.
	MembersMatch   bool   `json:"members_match"`
	RosterRevision uint64 `json:"roster_revision"`
}

func ValidateMQTTStorageAdjustment(q MQTTStorageAdjustment) error {
	if q.NodeID == 0 || q.MembershipRevision == 0 || q.ClusterLimit == 0 || q.ExpectedRevision == math.MaxUint64 ||
		q.ExpectedRevision == 0 && q.ExpectedBytes != 0 || q.TargetBytes > q.ClusterLimit {
		return dberrors.ErrInvalidArgument
	}
	if len(q.Members) == 0 || len(q.Members) > mqttStorageMaxNodes {
		return dberrors.ErrInvalidArgument
	}
	var last uint64
	for _, id := range q.Members {
		if id <= last {
			return dberrors.ErrInvalidArgument
		}
		last = id
	}
	if !slices.Contains(q.Members, q.NodeID) {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validateMQTTStorageLedger(r mqttStorageLedger) error {
	if r.Key != MQTTStorageRoutingKey || r.Limit == 0 || r.RosterRevision == 0 || len(r.Grants) > mqttStorageMaxNodes || len(r.Members) == 0 || len(r.Members) > mqttStorageMaxNodes {
		return dberrors.ErrCorruptValue
	}
	var lastMember uint64
	for _, id := range r.Members {
		if id <= lastMember {
			return dberrors.ErrCorruptValue
		}
		lastMember = id
	}
	var total, last uint64
	for _, g := range r.Grants {
		if g.NodeID <= last || g.Revision == 0 || g.Bytes > r.Limit-total {
			return dberrors.ErrCorruptValue
		}
		total += g.Bytes
		last = g.NodeID
	}
	return nil
}

var mqttStorageTable = registerMetaTable(TableSpec[mqttStorageLedger]{
	ID: TableIDMQTTStorageLedger, Name: "mqtt_storage_ledger",
	Columns: []schema.Column{{ID: 1, Name: "key", Type: schema.TypeString, Required: true},
		{ID: 2, Name: "limit", Type: schema.TypeUint64, Required: true},
		{ID: 3, Name: "grants", Type: schema.TypeBytes, Required: true}, {ID: 4, Name: "members", Type: schema.TypeBytes, Required: true}, {ID: 5, Name: "roster_revision", Type: schema.TypeUint64, Required: true}},
	Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{2, 3, 4, 5}}},
	Primary: PrimarySpec[mqttStorageLedger]{IndexID: 1, Name: "pk_mqtt_storage_ledger", Columns: []uint16{1},
		Layout: KeyLayout{KeyString}, Key: func(r mqttStorageLedger) KeyParts { return KeyParts{String(r.Key)} }},
	Validate: validateMQTTStorageLedger,
	EncodeValueWithKey: func(key []byte, r mqttStorageLedger) ([]byte, error) {
		p, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		value := rowcodec.Wrap(key, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, p)
		if len(value) > mqttStorageMaxLedgerBytes {
			return nil, dberrors.ErrInvalidArgument
		}
		return value, nil
	},
	DecodeValueWithKey: func(key []byte, pk KeyParts, v []byte) (mqttStorageLedger, error) {
		var r mqttStorageLedger
		if len(v) > mqttStorageMaxLedgerBytes || len(pk) != 1 || pk[0].S != MQTTStorageRoutingKey {
			return r, dberrors.ErrCorruptValue
		}
		e, err := rowcodec.UnwrapBorrowed(key, v)
		if err != nil {
			return r, err
		}
		if e.Version != 1 || e.Codec != rowcodec.CodecFixed || e.Flags != rowcodec.FlagChecksum {
			return r, dberrors.ErrCorruptValue
		}
		d := json.NewDecoder(bytes.NewReader(e.Payload))
		d.DisallowUnknownFields()
		if err = d.Decode(&r); err != nil {
			return r, dberrors.ErrCorruptValue
		}
		var extra any
		if d.Decode(&extra) != io.EOF || validateMQTTStorageLedger(r) != nil {
			return r, dberrors.ErrCorruptValue
		}
		return r, nil
	},
})

// AdjustMQTTStorage serializes node escrow and cluster capacity in one Slot
// transaction. Grant rows remain even at zero to prevent revision ABA.
func (b *Batch) AdjustMQTTStorage(slot HashSlot, q MQTTStorageAdjustment) (*MQTTStorageResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	if err := ValidateMQTTStorageAdjustment(q); err != nil {
		return nil, err
	}
	out := &MQTTStorageResult{}
	b.addOp(slot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		r, found, err := loadUpdateRow(mqttStorageTable, state, slot, KeyParts{String(MQTTStorageRoutingKey)})
		if err != nil {
			return err
		}
		if !found {
			r = mqttStorageLedger{Key: MQTTStorageRoutingKey, Limit: q.ClusterLimit, Members: slices.Clone(q.Members), RosterRevision: q.MembershipRevision}
		}
		var total uint64
		var debt bool
		for _, g := range r.Grants {
			total += g.Bytes
			debt = debt || g.Debt != 0
		}
		i := sort.Search(len(r.Grants), func(i int) bool { return r.Grants[i].NodeID >= q.NodeID })
		g := MQTTStorageGrant{NodeID: q.NodeID}
		present := i < len(r.Grants) && r.Grants[i].NodeID == q.NodeID
		if present {
			g = r.Grants[i]
		}
		match := slices.Equal(r.Members, q.Members)
		*out = MQTTStorageResult{Status: "conflict", Grant: g, Limit: r.Limit, Total: total, Ready: mqttStorageReady(r), MembersMatch: match, RosterRevision: r.RosterRevision}
		if q.MembershipRevision <= r.RosterRevision && g.Revision == q.ExpectedRevision+1 && g.Bytes == q.TargetBytes && (q.InitialDebt == nil || g.Initialized && g.Debt == *q.InitialDebt) {
			out.Status = "unchanged"
			return nil
		}
		if g.Revision != q.ExpectedRevision || g.Bytes != q.ExpectedBytes {
			return nil
		}
		// Forward Controller roster revisions retain every old grant/debt and
		// add an initialization barrier. Limit changes still require no debt.
		if !match && q.MembershipRevision > r.RosterRevision {
			r.Members = slices.Clone(q.Members)
			r.RosterRevision = q.MembershipRevision
			match = true
		}
		if match {
			r.RosterRevision = max(r.RosterRevision, q.MembershipRevision)
		}
		if r.Limit != q.ClusterLimit && total == 0 && !debt {
			r.Limit = q.ClusterLimit
		}
		status := "applied"
		target := q.TargetBytes
		if !match || r.Limit != q.ClusterLimit {
			status = "config_mismatch"
			if target > g.Bytes {
				target = g.Bytes
			}
		} else if target > g.Bytes && (target-g.Bytes > r.Limit-total || q.InitialDebt == nil && !mqttStorageReady(r)) {
			status = "full"
			target = g.Bytes
		}
		if !present && len(r.Grants) == mqttStorageMaxNodes {
			out.Status = "full"
			return nil
		}
		// A refusal still registers legacy debt, closing cluster-wide new admission.
		if q.InitialDebt != nil {
			g.Debt = *q.InitialDebt
			g.Initialized = true
		}
		if !g.Initialized {
			out.Status = "full"
			out.Ready = false
			return nil
		}
		if q.InitialDebt == nil && target < g.Debt {
			g.Debt = target
		}
		g.Revision++
		g.Bytes = target
		if present {
			r.Grants[i] = g
		} else {
			r.Grants = append(r.Grants, MQTTStorageGrant{})
			copy(r.Grants[i+1:], r.Grants[i:])
			r.Grants[i] = g
		}
		if err = stageUpdateRow(mqttStorageTable, state, batch, slot, r); err != nil {
			return err
		}
		*out = MQTTStorageResult{Status: status, Grant: g, Limit: r.Limit, Total: total - q.ExpectedBytes + g.Bytes, Ready: match && r.Limit == q.ClusterLimit && mqttStorageReady(r), MembersMatch: match, RosterRevision: r.RosterRevision}
		return nil
	})
	return out, nil
}

func mqttStorageReady(r mqttStorageLedger) bool {
	for _, id := range r.Members {
		i := sort.Search(len(r.Grants), func(i int) bool { return r.Grants[i].NodeID >= id })
		if i == len(r.Grants) || r.Grants[i].NodeID != id || !r.Grants[i].Initialized || r.Grants[i].Bytes < r.Grants[i].Debt {
			return false
		}
	}
	for _, g := range r.Grants {
		if g.Bytes < g.Debt {
			return false
		}
	}
	return true
}

// AdjustMQTTStorage exposes the identical atomic escrow operation to Slot FSMs.
func (b *WriteBatch) AdjustMQTTStorage(slot uint16, q MQTTStorageAdjustment) (*MQTTStorageResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.AdjustMQTTStorage(HashSlot(slot), q)
}

// inspectMQTTStorageRow exposes only bounded capacity evidence, never content.
func inspectMQTTStorageRow(r mqttStorageLedger) InspectRow {
	return InspectRow{"key": r.Key, "limit": r.Limit, "grants": r.Grants, "members": r.Members, "ready": mqttStorageReady(r)}
}
