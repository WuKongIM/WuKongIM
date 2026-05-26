package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const (
	pluginBindingPrimaryFamilyID uint16 = 0
	pluginBindingPrimaryIndexID  uint16 = 1
	pluginBindingPluginIndexID   uint16 = 2

	pluginBindingColumnUID      uint16 = 1
	pluginBindingColumnPluginNo uint16 = 2
	pluginBindingColumnValue    uint16 = 3
)

// PluginUserBinding records one cluster-authoritative UID to plugin binding.
type PluginUserBinding struct {
	// UID is the user id used as the routing key for this binding.
	UID string
	// PluginNo is the plugin selected for the UID.
	PluginNo string
	// CreatedAtMS records when the binding was first created.
	CreatedAtMS int64
	// UpdatedAtMS records when the binding was last updated.
	UpdatedAtMS int64
}

// PluginUserBindingCursor identifies the last emitted plugin binding.
type PluginUserBindingCursor struct {
	// PluginNo is the plugin number currently being scanned.
	PluginNo string
	// UID is the last emitted UID for PluginNo.
	UID string
}

var pluginBindingTable = registerMetaTable(TableSpec[PluginUserBinding]{
	ID:   TableIDPluginBinding,
	Name: "plugin_binding",
	Columns: []schema.Column{
		{ID: pluginBindingColumnUID, Name: "uid", Type: schema.TypeString, Required: true},
		{ID: pluginBindingColumnPluginNo, Name: "plugin_no", Type: schema.TypeString, Required: true},
		{ID: pluginBindingColumnValue, Name: "value", Type: schema.TypeBytes},
	},
	Families: []schema.Family{{ID: pluginBindingPrimaryFamilyID, Name: "primary", Columns: []uint16{pluginBindingColumnValue}}},
	Primary: PrimarySpec[PluginUserBinding]{
		IndexID:  pluginBindingPrimaryIndexID,
		FamilyID: pluginBindingPrimaryFamilyID,
		Name:     "pk_plugin_binding",
		Columns:  []uint16{pluginBindingColumnUID, pluginBindingColumnPluginNo},
		Layout:   KeyLayout{KeyString, KeyString},
		Key: func(binding PluginUserBinding) KeyParts {
			return KeyParts{String(binding.UID), String(binding.PluginNo)}
		},
	},
	Indexes: []IndexSpec[PluginUserBinding]{
		{
			ID:      pluginBindingPluginIndexID,
			Name:    "idx_plugin_binding_plugin_no",
			Columns: []uint16{pluginBindingColumnPluginNo, pluginBindingColumnUID},
			Layout:  KeyLayout{KeyString, KeyString},
			Key: func(binding PluginUserBinding) (KeyParts, bool) {
				return KeyParts{String(binding.PluginNo), String(binding.UID)}, true
			},
		},
	},
	Validate: validatePluginUserBinding,
	EncodeValue: func(binding PluginUserBinding) ([]byte, error) {
		return encodePluginUserBindingValue(binding), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (PluginUserBinding, error) {
		return decodePluginUserBindingValue(primary[0].S, primary[1].S, value)
	},
})

// PluginBindingTable describes the plugin binding table schema.
var PluginBindingTable = pluginBindingTable.Schema()

// BindPluginUser creates or updates one UID to plugin binding idempotently.
func (s *Shard) BindPluginUser(ctx context.Context, binding PluginUserBinding) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validatePluginUserBinding(binding); err != nil {
		return err
	}
	existing, exists, err := s.GetPluginUserBinding(ctx, binding.UID, binding.PluginNo)
	if err != nil {
		return err
	}
	if exists {
		binding.CreatedAtMS = existing.CreatedAtMS
		if binding.UpdatedAtMS < existing.UpdatedAtMS {
			binding.UpdatedAtMS = existing.UpdatedAtMS
		}
	}
	return pluginBindingTable.Upsert(ctx, s, binding)
}

// UnbindPluginUser removes one UID to plugin binding idempotently.
func (s *Shard) UnbindPluginUser(ctx context.Context, uid, pluginNo string) error {
	if err := validatePluginUserBindingIdentity(uid, pluginNo); err != nil {
		return err
	}
	return pluginBindingTable.Delete(ctx, s, KeyParts{String(uid), String(pluginNo)})
}

// ListPluginBindingsByUID lists all plugin bindings for a UID in plugin order.
func (s *Shard) ListPluginBindingsByUID(ctx context.Context, uid string) ([]PluginUserBinding, error) {
	if err := validatePluginBindingUID(uid); err != nil {
		return nil, err
	}
	rows, _, _, err := pluginBindingTable.ScanPrimaryPrefix(ctx, s, KeyParts{String(uid)}, nil, 0)
	return rows, err
}

// ScanPluginBindingsByPluginNo scans plugin bindings by plugin number and UID.
func (s *Shard) ScanPluginBindingsByPluginNo(ctx context.Context, pluginNo string, cursor PluginUserBindingCursor, limit int) ([]PluginUserBinding, PluginUserBindingCursor, bool, error) {
	if err := validatePluginBindingPluginNo(pluginNo); err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	if err := validatePluginUserBindingCursor(pluginNo, cursor); err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	if limit <= 0 {
		return nil, PluginUserBindingCursor{}, false, dberrors.ErrInvalidArgument
	}
	var after KeyParts
	if cursor.UID != "" {
		after = KeyParts{String(pluginNo), String(cursor.UID)}
	}
	rows, next, done, err := pluginBindingTable.ScanIndex(ctx, s, pluginBindingPluginIndexID, KeyParts{String(pluginNo)}, after, limit)
	if err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	nextCursor := cursor
	if len(next) >= 2 {
		nextCursor = PluginUserBindingCursor{PluginNo: pluginNo, UID: next[1].S}
	} else if len(rows) > 0 {
		nextCursor = pluginBindingToCursor(rows[len(rows)-1])
	}
	return rows, nextCursor, done, nil
}

// ExistPluginBindingByUID reports whether a UID has at least one plugin binding.
func (s *Shard) ExistPluginBindingByUID(ctx context.Context, uid string) (bool, error) {
	if err := validatePluginBindingUID(uid); err != nil {
		return false, err
	}
	rows, _, _, err := pluginBindingTable.ScanPrimaryPrefix(ctx, s, KeyParts{String(uid)}, nil, 1)
	return len(rows) > 0, err
}

// GetPluginUserBinding returns one UID to plugin binding.
func (s *Shard) GetPluginUserBinding(ctx context.Context, uid, pluginNo string) (PluginUserBinding, bool, error) {
	if err := validatePluginUserBindingIdentity(uid, pluginNo); err != nil {
		return PluginUserBinding{}, false, err
	}
	return pluginBindingTable.Get(ctx, s, KeyParts{String(uid), String(pluginNo)})
}

func validatePluginUserBinding(binding PluginUserBinding) error {
	if err := validatePluginUserBindingIdentity(binding.UID, binding.PluginNo); err != nil {
		return err
	}
	if binding.UpdatedAtMS < binding.CreatedAtMS {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validatePluginUserBindingIdentity(uid, pluginNo string) error {
	if err := validatePluginBindingUID(uid); err != nil {
		return err
	}
	return validatePluginBindingPluginNo(pluginNo)
}

func validatePluginBindingUID(uid string) error {
	return validateKeyString(uid)
}

func validatePluginBindingPluginNo(pluginNo string) error {
	return validateKeyString(pluginNo)
}

func validatePluginUserBindingCursor(pluginNo string, cursor PluginUserBindingCursor) error {
	if cursor == (PluginUserBindingCursor{}) {
		return nil
	}
	if cursor.PluginNo != pluginNo || cursor.UID == "" {
		return dberrors.ErrInvalidArgument
	}
	return validatePluginBindingUID(cursor.UID)
}

func pluginBindingToCursor(binding PluginUserBinding) PluginUserBindingCursor {
	return PluginUserBindingCursor{PluginNo: binding.PluginNo, UID: binding.UID}
}

func encodePluginUserBindingValue(binding PluginUserBinding) []byte {
	value := appendValueInt64(nil, binding.CreatedAtMS)
	return appendValueInt64(value, binding.UpdatedAtMS)
}

func decodePluginUserBindingValue(uid, pluginNo string, value []byte) (PluginUserBinding, error) {
	createdAt, rest, err := readValueInt64(value)
	if err != nil {
		return PluginUserBinding{}, err
	}
	updatedAt, rest, err := readValueInt64(rest)
	if err != nil {
		return PluginUserBinding{}, err
	}
	if len(rest) != 0 {
		return PluginUserBinding{}, dberrors.ErrCorruptValue
	}
	return PluginUserBinding{UID: uid, PluginNo: pluginNo, CreatedAtMS: createdAt, UpdatedAtMS: updatedAt}, nil
}
