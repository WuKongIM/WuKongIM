package hashslot

import "errors"

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

var ErrInvalidTable = errors.New("hashslot: invalid table")

const hashSlotTableEncodingVersion uint16 = 2

type MigrationPhase uint8

const (
	PhaseSnapshot MigrationPhase = iota
	PhaseDelta
	PhaseSwitching
	PhaseDone
)

type HashSlotMigration struct {
	HashSlot uint16
	Source   multiraft.SlotID
	Target   multiraft.SlotID
	Phase    MigrationPhase
}

type HashSlotTable struct {
	version       uint64
	hashSlotCount uint16
	assignment    []multiraft.SlotID
	migrations    map[uint16]HashSlotMigration
}

func NewHashSlotTable(hashSlotCount uint16, physicalSlotCount int) *HashSlotTable {
	table := &HashSlotTable{
		version:       1,
		hashSlotCount: hashSlotCount,
		assignment:    make([]multiraft.SlotID, hashSlotCount),
		migrations:    make(map[uint16]HashSlotMigration),
	}
	if hashSlotCount == 0 || physicalSlotCount <= 0 {
		return table
	}

	base := int(hashSlotCount) / physicalSlotCount
	remainder := int(hashSlotCount) % physicalSlotCount
	next := uint16(0)
	for slotIdx := 0; slotIdx < physicalSlotCount; slotIdx++ {
		count := base
		if slotIdx < remainder {
			count++
		}
		slotID := multiraft.SlotID(slotIdx + 1)
		for i := 0; i < count && next < hashSlotCount; i++ {
			table.assignment[next] = slotID
			next++
		}
	}
	return table
}

func (t *HashSlotTable) Lookup(hashSlot uint16) multiraft.SlotID {
	if t == nil || int(hashSlot) >= len(t.assignment) {
		return 0
	}
	return t.assignment[hashSlot]
}

func (t *HashSlotTable) Reassign(hashSlot uint16, slotID multiraft.SlotID) {
	if t == nil || int(hashSlot) >= len(t.assignment) {
		return
	}
	if t.assignment[hashSlot] == slotID {
		return
	}
	t.assignment[hashSlot] = slotID
	t.version++
}

func (t *HashSlotTable) StartMigration(hashSlot uint16, sourceSlot, targetSlot multiraft.SlotID) {
	if t == nil || int(hashSlot) >= len(t.assignment) {
		return
	}
	current := t.assignment[hashSlot]
	if sourceSlot == 0 || targetSlot == 0 || sourceSlot == targetSlot || current != sourceSlot {
		return
	}
	migration := HashSlotMigration{
		HashSlot: hashSlot,
		Source:   sourceSlot,
		Target:   targetSlot,
		Phase:    PhaseSnapshot,
	}
	if existing, ok := t.migrations[hashSlot]; ok && existing == migration {
		return
	}
	if t.migrations == nil {
		t.migrations = make(map[uint16]HashSlotMigration)
	}
	t.migrations[hashSlot] = migration
	t.version++
}

func (t *HashSlotTable) AdvanceMigration(hashSlot uint16, phase MigrationPhase) {
	if t == nil {
		return
	}
	migration, ok := t.migrations[hashSlot]
	if !ok || migration.Phase == phase {
		return
	}
	migration.Phase = phase
	t.migrations[hashSlot] = migration
	t.version++
}

func (t *HashSlotTable) FinalizeMigration(hashSlot uint16) {
	if t == nil {
		return
	}
	migration, ok := t.migrations[hashSlot]
	if !ok {
		return
	}
	if int(hashSlot) < len(t.assignment) {
		t.assignment[hashSlot] = migration.Target
	}
	delete(t.migrations, hashSlot)
	t.version++
}

func (t *HashSlotTable) AbortMigration(hashSlot uint16) {
	if t == nil {
		return
	}
	if _, ok := t.migrations[hashSlot]; !ok {
		return
	}
	delete(t.migrations, hashSlot)
	t.version++
}

func (t *HashSlotTable) GetMigration(hashSlot uint16) *HashSlotMigration {
	if t == nil {
		return nil
	}
	migration, ok := t.migrations[hashSlot]
	if !ok {
		return nil
	}
	migrationCopy := migration
	return &migrationCopy
}

func (t *HashSlotTable) ActiveMigrations() []HashSlotMigration {
	if t == nil || len(t.migrations) == 0 {
		return nil
	}
	out := make([]HashSlotMigration, 0, len(t.migrations))
	for _, migration := range t.migrations {
		out = append(out, migration)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].HashSlot < out[j].HashSlot
	})
	return out
}

func (t *HashSlotTable) HashSlotsOf(slotID multiraft.SlotID) []uint16 {
	if t == nil {
		return nil
	}
	out := make([]uint16, 0, len(t.assignment))
	for hashSlot, assigned := range t.assignment {
		if assigned == slotID {
			out = append(out, uint16(hashSlot))
		}
	}
	return out
}

func (t *HashSlotTable) Version() uint64 {
	if t == nil {
		return 0
	}
	return t.version
}

func (t *HashSlotTable) HashSlotCount() uint16 {
	if t == nil {
		return 0
	}
	return t.hashSlotCount
}

func (t *HashSlotTable) Clone() *HashSlotTable {
	if t == nil {
		return nil
	}
	cloned := &HashSlotTable{
		version:       t.version,
		hashSlotCount: t.hashSlotCount,
		assignment:    make([]multiraft.SlotID, len(t.assignment)),
		migrations:    make(map[uint16]HashSlotMigration, len(t.migrations)),
	}
	copy(cloned.assignment, t.assignment)
	for hashSlot, migration := range t.migrations {
		cloned.migrations[hashSlot] = migration
	}
	return cloned
}

func (t *HashSlotTable) Encode() []byte {
	if t == nil {
		return nil
	}

	migrations := t.ActiveMigrations()
	data := make([]byte, 0, 2+2+8+len(t.assignment)*8+2+len(migrations)*20)
	data = binary.BigEndian.AppendUint16(data, hashSlotTableEncodingVersion)
	data = binary.BigEndian.AppendUint16(data, t.hashSlotCount)
	data = binary.BigEndian.AppendUint64(data, t.version)
	for _, slotID := range t.assignment {
		data = binary.BigEndian.AppendUint64(data, uint64(slotID))
	}
	data = binary.BigEndian.AppendUint16(data, uint16(len(migrations)))
	for _, migration := range migrations {
		data = binary.BigEndian.AppendUint16(data, migration.HashSlot)
		data = append(data, byte(migration.Phase), 0)
		data = binary.BigEndian.AppendUint64(data, uint64(migration.Source))
		data = binary.BigEndian.AppendUint64(data, uint64(migration.Target))
	}
	return data
}

func DecodeHashSlotTable(data []byte) (*HashSlotTable, error) {
	const headerSize = 2 + 2 + 8
	if len(data) < headerSize {
		return nil, fmt.Errorf("%w: hash slot table payload too short", ErrInvalidTable)
	}

	version := binary.BigEndian.Uint16(data[:2])
	if version != 1 && version != hashSlotTableEncodingVersion {
		return nil, fmt.Errorf("%w: unknown hash slot table version %d", ErrInvalidTable, version)
	}

	hashSlotCount := binary.BigEndian.Uint16(data[2:4])
	table := &HashSlotTable{
		version:       binary.BigEndian.Uint64(data[4:12]),
		hashSlotCount: hashSlotCount,
		assignment:    make([]multiraft.SlotID, hashSlotCount),
		migrations:    make(map[uint16]HashSlotMigration),
	}
	offset := headerSize
	for i := range table.assignment {
		if len(data) < offset+8 {
			return nil, fmt.Errorf("%w: hash slot table payload length mismatch", ErrInvalidTable)
		}
		table.assignment[i] = multiraft.SlotID(binary.BigEndian.Uint64(data[offset : offset+8]))
		offset += 8
	}
	if version == 1 {
		if len(data) != offset {
			return nil, fmt.Errorf("%w: hash slot table payload length mismatch", ErrInvalidTable)
		}
		return table, nil
	}

	if len(data) == offset {
		return table, nil
	}
	if len(data) < offset+2 {
		return nil, fmt.Errorf("%w: hash slot table payload length mismatch", ErrInvalidTable)
	}
	migrationCount := binary.BigEndian.Uint16(data[offset : offset+2])
	offset += 2
	const migrationRecordSize = 20
	wantLen := offset + int(migrationCount)*migrationRecordSize
	if len(data) != wantLen {
		return nil, fmt.Errorf("%w: hash slot table payload length mismatch", ErrInvalidTable)
	}
	for i := 0; i < int(migrationCount); i++ {
		hashSlot := binary.BigEndian.Uint16(data[offset : offset+2])
		phase := MigrationPhase(data[offset+2])
		sourceSlot := multiraft.SlotID(binary.BigEndian.Uint64(data[offset+4 : offset+12]))
		targetSlot := multiraft.SlotID(binary.BigEndian.Uint64(data[offset+12 : offset+20]))
		table.migrations[hashSlot] = HashSlotMigration{
			HashSlot: hashSlot,
			Source:   sourceSlot,
			Target:   targetSlot,
			Phase:    phase,
		}
		offset += migrationRecordSize
	}
	return table, nil
}

func HashSlotForKey(key string, hashSlotCount uint16) uint16 {
	if hashSlotCount == 0 {
		return 0
	}
	return uint16(crc32.ChecksumIEEE([]byte(key)) % uint32(hashSlotCount))
}
