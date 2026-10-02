package transfer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSubscriberIncarnationOfflineTransfer(t *testing.T) {
	ctx := context.Background()
	const slots = 16
	source, opts := seedVerifyNodeStore(t, slots, verifySeedOptions{})
	hs := testHashSlot("g1", slots)
	emptySlot := (hs + 1) % slots
	s := source.Meta().HashSlot(hs)
	if err := s.ImportSubscriberSequence(ctx, 9007199254740993); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSubscribers(ctx, "g1", 2, []string{"u2"}, 0); err != nil {
		t.Fatal(err)
	}
	original, ok, err := s.GetSubscriber(ctx, "g1", 2, "u2")
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err = source.Meta().HashSlot(emptySlot).ImportSubscriberSequence(ctx, 9007199254741011); err != nil {
		t.Fatal(err)
	}
	closeVerifyNodeStore(t, source)
	src := openVerifyInspectStore(t, opts, slots)
	root := filepath.Join(t.TempDir(), "bundle")
	if _, err = ExportBundle(ctx, root, src, ExportOptions{HashSlotCount: slots, PageSize: 1}); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if countManifestKind(manifest, FileKindMetaSubscriberSequences) != 1 {
		t.Fatal("allocator dataset absent")
	}
	target, targetOpts := openExportNodeStore(t, t.TempDir())
	if _, err = ImportBundle(ctx, root, target, ImportOptions{HashSlotCount: slots, RequireEmpty: true, SubscriberBatchSize: 1}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := target.Meta().HashSlot(hs).GetSubscriber(ctx, "g1", 2, "u2")
	if err != nil || !ok || got != original {
		t.Fatalf("row=%+v want=%+v err=%v", got, original, err)
	}
	high, err := target.Meta().HashSlot(emptySlot).SubscriberSequence(ctx)
	if err != nil || high != 9007199254741011 {
		t.Fatalf("empty Slot high water=%d %v", high, err)
	}
	closeVerifyNodeStore(t, target)
	dst := openVerifyInspectStore(t, targetOpts, slots)
	for _, mode := range []VerifyMode{VerifyModeSummary, VerifyModeFull} {
		report, err := VerifyStores(ctx, src, dst, VerifyOptions{HashSlotCount: slots, Mode: mode})
		if err != nil || !report.Equal {
			t.Fatalf("verify=%+v %v", report, err)
		}
	}
	// Removing the sequence witness cannot make a new-format bundle legacy.
	files := manifest.Files[:0]
	for _, e := range manifest.Files {
		if e.Kind != FileKindMetaSubscriberSequences {
			files = append(files, e)
		}
	}
	manifest.Files = files
	if err = os.Remove(filepath.Join(root, manifestFileName)); err != nil {
		t.Fatal(err)
	}
	if err = writeExportManifest(root, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err = ValidateBundle(ctx, root, ImportOptions{HashSlotCount: slots}); err == nil {
		t.Fatal("missing allocator accepted")
	}
}

func TestSubscriberIncarnationBundleWitnessValidation(t *testing.T) {
	hs := testHashSlot("g1", 16)
	for _, tc := range []struct {
		name   string
		seq    []SubscriberSequenceRecord
		member uint64
		bad    bool
	}{
		{"legacy", nil, 0, false},
		{"missing", nil, 8, true},
		{"low", []SubscriberSequenceRecord{{HashSlot: hs, Sequence: 7}}, 8, true},
		{"exact", []SubscriberSequenceRecord{{HashSlot: hs, Sequence: 8}}, 8, false},
		{"duplicate", []SubscriberSequenceRecord{{HashSlot: hs, Sequence: 8}, {HashSlot: hs, Sequence: 8}}, 8, true},
		{"foreign", []SubscriberSequenceRecord{{HashSlot: 16, Sequence: 8}}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newBundleValidator(16)
			var err error
			for _, r := range tc.seq {
				if err = v.Visit(FileKindMetaSubscriberSequences, r); err != nil {
					break
				}
			}
			if err == nil {
				err = v.Visit(FileKindMetaSubscribers, SubscriberRecord{HashSlot: hs, ChannelID: "g1", ChannelType: 2, UID: "u", Incarnation: Uint64(tc.member)})
			}
			if err == nil {
				err = v.finishSubscriberSequences()
			}
			if (err != nil) != tc.bad {
				t.Fatalf("err=%v bad=%v", err, tc.bad)
			}
		})
	}

}

// A retained allocation floor is business state even when every member was deleted.
func TestSubscriberIncarnationImportEmptyTargetRejectsSequenceOnlyState(t *testing.T) {
	ctx := context.Background()
	const slots = 16
	source, opts := seedVerifyNodeStore(t, slots, verifySeedOptions{})
	closeVerifyNodeStore(t, source)
	src := openVerifyInspectStore(t, opts, slots)
	root := filepath.Join(t.TempDir(), "bundle")
	if _, err := ExportBundle(ctx, root, src, ExportOptions{HashSlotCount: slots}); err != nil {
		t.Fatal(err)
	}
	target, _ := openExportNodeStore(t, t.TempDir())
	hs := testHashSlot("g1", slots)
	if err := target.Meta().HashSlot(hs).ImportSubscriberSequence(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportBundle(ctx, root, target, ImportOptions{HashSlotCount: slots, RequireEmpty: true}); err == nil {
		t.Fatal("sequence-only target was treated as empty")
	}
}
