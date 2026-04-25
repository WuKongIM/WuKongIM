package meta

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type stressConfig struct {
	Enabled  bool
	Duration time.Duration
	Workers  int
	Slots    int
	Seed     int64
}

type channelKey struct {
	ChannelID   string
	ChannelType int64
}

type stressModel struct {
	mu       sync.RWMutex
	users    map[uint64]map[string]User
	channels map[uint64]map[channelKey]Channel
}

func TestStressConfigDefaultsAndOverrides(t *testing.T) {
	t.Setenv("WKDB_STRESS_DURATION", "1500ms")
	t.Setenv("WKDB_STRESS_WORKERS", "6")
	t.Setenv("WKDB_STRESS_SLOTS", "5")
	t.Setenv("WKDB_STRESS_SEED", "42")

	cfg := loadStressConfig(t)

	if cfg.Duration != 1500*time.Millisecond || cfg.Workers != 6 || cfg.Slots != 5 || cfg.Seed != 42 {
		t.Fatalf("cfg = %#v", cfg)
	}
}

func TestStressReferenceModelChannelOrdering(t *testing.T) {
	model := newStressModel()
	model.putChannel(7, Channel{ChannelID: "group-1", ChannelType: 3, Ban: 1})
	model.putChannel(7, Channel{ChannelID: "group-1", ChannelType: 1, Ban: 0})

	got := model.listChannels(7, "group-1")
	want := []Channel{
		{ChannelID: "group-1", ChannelType: 1, Ban: 0},
		{ChannelID: "group-1", ChannelType: 3, Ban: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listChannels() = %#v, want %#v", got, want)
	}
}

func openTestDB(tb testing.TB) *DB {
	tb.Helper()

	return openTestDBAt(tb, filepath.Join(tb.TempDir(), "db"))
}

func openTestDBAt(tb testing.TB, path string) *DB {
	tb.Helper()

	db, err := Open(path)
	if err != nil {
		tb.Fatalf("open db: %v", err)
	}
	tb.Cleanup(func() {
		if err := db.Close(); err != nil {
			tb.Fatalf("close db: %v", err)
		}
	})
	return db
}

func loadStressConfig(tb testing.TB) stressConfig {
	tb.Helper()

	cfg := stressConfig{
		Enabled:  envBool("WKDB_STRESS", false),
		Duration: envDuration(tb, "WKDB_STRESS_DURATION", 2*time.Second),
		Workers:  envInt(tb, "WKDB_STRESS_WORKERS", max(4, runtime.GOMAXPROCS(0))),
		Slots:    envInt(tb, "WKDB_STRESS_SLOTS", 4),
		Seed:     envInt64(tb, "WKDB_STRESS_SEED", 20260327),
	}
	if cfg.Workers <= 0 {
		tb.Fatalf("WKDB_STRESS_WORKERS must be > 0, got %d", cfg.Workers)
	}
	if cfg.Slots <= 0 {
		tb.Fatalf("WKDB_STRESS_SLOTS must be > 0, got %d", cfg.Slots)
	}
	if cfg.Duration <= 0 {
		tb.Fatalf("WKDB_STRESS_DURATION must be > 0, got %s", cfg.Duration)
	}
	return cfg
}

func newStressModel() *stressModel {
	return &stressModel{
		users:    make(map[uint64]map[string]User),
		channels: make(map[uint64]map[channelKey]Channel),
	}
}

func (m *stressModel) putUser(slot uint64, u User) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.users[slot] == nil {
		m.users[slot] = make(map[string]User)
	}
	m.users[slot][u.UID] = u
}

func (m *stressModel) deleteUser(slot uint64, uid string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.users[slot] == nil {
		return
	}
	delete(m.users[slot], uid)
}

func (m *stressModel) putChannel(slot uint64, ch Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.channels[slot] == nil {
		m.channels[slot] = make(map[channelKey]Channel)
	}
	m.channels[slot][channelKey{
		ChannelID:   ch.ChannelID,
		ChannelType: ch.ChannelType,
	}] = ch
}

func (m *stressModel) deleteChannel(slot uint64, channelID string, channelType int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.channels[slot] == nil {
		return
	}
	delete(m.channels[slot], channelKey{
		ChannelID:   channelID,
		ChannelType: channelType,
	})
}

func (m *stressModel) listChannels(slot uint64, channelID string) []Channel {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := m.listChannelsLocked(slot, channelID)
	return append([]Channel(nil), list...)
}

func (m *stressModel) snapshotUsers(slot uint64) map[string]User {
	m.mu.RLock()
	defer m.mu.RUnlock()

	users := make(map[string]User, len(m.users[slot]))
	for uid, user := range m.users[slot] {
		users[uid] = user
	}
	return users
}

func (m *stressModel) snapshotChannels(slot uint64) map[channelKey]Channel {
	m.mu.RLock()
	defer m.mu.RUnlock()

	channels := make(map[channelKey]Channel, len(m.channels[slot]))
	for key, ch := range m.channels[slot] {
		channels[key] = ch
	}
	return channels
}

func (m *stressModel) listChannelsLocked(slot uint64, channelID string) []Channel {
	var list []Channel
	for key, ch := range m.channels[slot] {
		if key.ChannelID == channelID {
			list = append(list, ch)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].ChannelType < list[j].ChannelType
	})
	return list
}

func assertSlotMatchesModel(t *testing.T, db *DB, slot uint64, model *stressModel) {
	t.Helper()

	ctx := context.Background()
	shard := db.ForSlot(slot)
	users := model.snapshotUsers(slot)
	channels := model.snapshotChannels(slot)

	for uid, want := range users {
		got, err := shard.GetUser(ctx, uid)
		if err != nil {
			t.Fatalf("GetUser(slot=%d, uid=%q): %v", slot, uid, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("GetUser(slot=%d, uid=%q) = %#v, want %#v", slot, uid, got, want)
		}
	}

	channelIDs := make(map[string]struct{})
	for _, ch := range channels {
		got, err := shard.GetChannel(ctx, ch.ChannelID, ch.ChannelType)
		if err != nil {
			t.Fatalf("GetChannel(slot=%d, channelID=%q, type=%d): %v", slot, ch.ChannelID, ch.ChannelType, err)
		}
		if !reflect.DeepEqual(got, ch) {
			t.Fatalf("GetChannel(slot=%d, channelID=%q, type=%d) = %#v, want %#v", slot, ch.ChannelID, ch.ChannelType, got, ch)
		}
		channelIDs[ch.ChannelID] = struct{}{}
	}

	for channelID := range channelIDs {
		got, err := shard.ListChannelsByChannelID(ctx, channelID)
		if err != nil {
			t.Fatalf("ListChannelsByChannelID(slot=%d, channelID=%q): %v", slot, channelID, err)
		}
		want := model.listChannels(slot, channelID)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ListChannelsByChannelID(slot=%d, channelID=%q) = %#v, want %#v", slot, channelID, got, want)
		}
	}

	snap, err := db.ExportSlotSnapshot(ctx, slot)
	if err != nil {
		t.Fatalf("ExportSlotSnapshot(slot=%d): %v", slot, err)
	}
	decoded, err := decodeSlotSnapshotPayload(snap.Data)
	if err != nil {
		t.Fatalf("decodeSlotSnapshotPayload(slot=%d): %v", slot, err)
	}
	if decoded.SlotID != slot {
		t.Fatalf("decoded snapshot slot = %d, want %d", decoded.SlotID, slot)
	}

	actual := make(map[string][]byte, len(decoded.Entries))
	for _, entry := range decoded.Entries {
		actual[string(entry.Key)] = append([]byte(nil), entry.Value...)
	}

	expected := make(map[string][]byte, len(users)+2*len(channels))
	hashSlot := uint16(slot)
	for uid, user := range users {
		key := encodeUserPrimaryKey(hashSlot, uid, userPrimaryFamilyID)
		expected[string(key)] = encodeUserFamilyValue(user.Token, user.DeviceFlag, user.DeviceLevel, key)
	}
	for _, ch := range channels {
		primaryKey := encodeChannelPrimaryKey(hashSlot, ch.ChannelID, ch.ChannelType, channelPrimaryFamilyID)
		expected[string(primaryKey)] = encodeChannelFamilyValue(ch.Ban, primaryKey)

		indexKey := encodeChannelIDIndexKey(hashSlot, ch.ChannelID, ch.ChannelType)
		expected[string(indexKey)] = encodeChannelIndexValue(ch.Ban)
	}

	if len(actual) != len(expected) {
		t.Fatalf("snapshot entry count(slot=%d) = %d, want %d", slot, len(actual), len(expected))
	}
	for key, want := range expected {
		got, ok := actual[key]
		if !ok {
			t.Fatalf("missing snapshot entry(slot=%d, key=%x)", slot, []byte(key))
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("snapshot entry mismatch(slot=%d, key=%x): got=%x want=%x", slot, []byte(key), got, want)
		}
	}
}

func envBool(name string, fallback bool) bool {
	value, ok := os.LookupEnv(name)
	if !ok || value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envDuration(tb testing.TB, name string, fallback time.Duration) time.Duration {
	tb.Helper()

	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		tb.Fatalf("parse %s: %v", name, err)
	}
	return d
}

func envInt(tb testing.TB, name string, fallback int) int {
	tb.Helper()

	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		tb.Fatalf("parse %s: %v", name, err)
	}
	return n
}

func envInt64(tb testing.TB, name string, fallback int64) int64 {
	tb.Helper()

	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		tb.Fatalf("parse %s: %v", name, err)
	}
	return n
}
