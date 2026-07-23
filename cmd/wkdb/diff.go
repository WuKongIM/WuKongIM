package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// diffCommandFlags contains flags accepted after `wkdb diff`.
type diffCommandFlags struct {
	// sourceDataDir derives source metadata and message paths from a node data directory.
	sourceDataDir string
	// sourceMetaPath overrides the source metadata Pebble path.
	sourceMetaPath string
	// sourceMessagePath overrides the source message Pebble path.
	sourceMessagePath string
	// targetDataDir derives target metadata and message paths from a node data directory.
	targetDataDir string
	// targetMetaPath overrides the target metadata Pebble path.
	targetMetaPath string
	// targetMessagePath overrides the target message Pebble path.
	targetMessagePath string
	// mode selects summary or full digest comparison.
	mode string
	// pageSize bounds inspect scan rows per verify page.
	pageSize int
}

func runDiff(ctx context.Context, global cliFlags, args []string, stdout, stderr io.Writer) int {
	diffFlags, code := parseDiffCommandFlags(args, stderr)
	if code != exitOK {
		return code
	}
	format := firstNonEmpty(global.format, "table")
	if !validFormat(format) {
		fmt.Fprintf(stderr, "config error: unknown format %q\n", format)
		return exitConfig
	}
	hashSlotCount, err := resolveCLIHashSlotCount(global, os.Environ())
	if err != nil {
		fmt.Fprintf(stderr, "config error: %v\n", err)
		return exitConfig
	}
	if hashSlotCount == 0 {
		fmt.Fprintln(stderr, "config error: diff requires --hash-slot-count or WK_CLUSTER_HASH_SLOT_COUNT")
		return exitConfig
	}

	sourceOpts := diffFlags.sourceInspectOptions(hashSlotCount)
	targetOpts := diffFlags.targetInspectOptions(hashSlotCount)
	if sourceOpts.MetaPath == "" || sourceOpts.MessagePath == "" {
		fmt.Fprintln(stderr, "diff error: source requires metadata and message storage paths")
		return exitConfig
	}
	if targetOpts.MetaPath == "" || targetOpts.MessagePath == "" {
		fmt.Fprintln(stderr, "diff error: target requires metadata and message storage paths")
		return exitConfig
	}

	source, err := inspect.OpenStore(sourceOpts)
	if err != nil {
		fmt.Fprintf(stderr, "open source store: %v\n", err)
		return exitConfig
	}
	target, err := inspect.OpenStore(targetOpts)
	if err != nil {
		_ = source.Close()
		fmt.Fprintf(stderr, "open target store: %v\n", err)
		return exitConfig
	}

	report, verifyErr := transfer.VerifyStores(ctx, source, target, diffFlags.transferOptions(hashSlotCount))
	closeErr := errors.Join(source.Close(), target.Close())
	if verifyErr != nil {
		fmt.Fprintf(stderr, "diff store: %v\n", verifyErr)
		return diffTransferExitCode(verifyErr)
	}
	if closeErr != nil {
		fmt.Fprintf(stderr, "close store: %v\n", closeErr)
		return exitInternal
	}
	if err := renderDiffReport(stdout, format, report); err != nil {
		fmt.Fprintf(stderr, "render error: %v\n", err)
		return exitInternal
	}
	if !report.Equal {
		return exitQuery
	}
	return exitOK
}

func parseDiffCommandFlags(args []string, stderr io.Writer) (diffCommandFlags, int) {
	var flags diffCommandFlags
	fs := flag.NewFlagSet("wkdb diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&flags.sourceDataDir, "source-data-dir", "", "source node data directory")
	fs.StringVar(&flags.sourceMetaPath, "source-meta-path", "", "source metadata store path")
	fs.StringVar(&flags.sourceMessagePath, "source-message-path", "", "source message store path")
	fs.StringVar(&flags.targetDataDir, "target-data-dir", "", "target node data directory")
	fs.StringVar(&flags.targetMetaPath, "target-meta-path", "", "target metadata store path")
	fs.StringVar(&flags.targetMessagePath, "target-message-path", "", "target message store path")
	fs.StringVar(&flags.mode, "mode", "", "comparison mode: summary, full")
	fs.IntVar(&flags.pageSize, "page-size", 0, "rows per read-only diff scan page")
	if err := fs.Parse(args); err != nil {
		return diffCommandFlags{}, exitConfig
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "diff error: unexpected argument %q\n", fs.Arg(0))
		return diffCommandFlags{}, exitConfig
	}
	if flags.pageSize < 0 {
		fmt.Fprintln(stderr, "diff error: page size must be non-negative")
		return diffCommandFlags{}, exitConfig
	}
	if flags.mode != "" && flags.mode != string(transfer.VerifyModeSummary) && flags.mode != string(transfer.VerifyModeFull) {
		fmt.Fprintf(stderr, "diff error: unknown mode %q\n", flags.mode)
		return diffCommandFlags{}, exitConfig
	}
	return flags, exitOK
}

func (f diffCommandFlags) sourceInspectOptions(hashSlotCount uint16) inspect.Options {
	return diffInspectOptions(f.sourceDataDir, f.sourceMetaPath, f.sourceMessagePath, hashSlotCount)
}

func (f diffCommandFlags) targetInspectOptions(hashSlotCount uint16) inspect.Options {
	return diffInspectOptions(f.targetDataDir, f.targetMetaPath, f.targetMessagePath, hashSlotCount)
}

func diffInspectOptions(dataDir, metaPath, messagePath string, hashSlotCount uint16) inspect.Options {
	dataDir = strings.TrimSpace(dataDir)
	return inspect.Options{
		MetaPath:      resolveStoragePath(metaPath, dataDir, "", dataDir, defaultMetaStoreDirName),
		MessagePath:   resolveStoragePath(messagePath, dataDir, "", dataDir, defaultMessageStoreDirName),
		HashSlotCount: hashSlotCount,
		DefaultLimit:  100,
		MaxLimit:      10000,
	}
}

func (f diffCommandFlags) transferOptions(hashSlotCount uint16) transfer.VerifyOptions {
	return transfer.VerifyOptions{
		HashSlotCount: hashSlotCount,
		PageSize:      f.pageSize,
		Mode:          transfer.VerifyMode(f.mode),
	}
}

func renderDiffReport(stdout io.Writer, format string, report transfer.VerifyReport) error {
	switch format {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	case "jsonl":
		return renderDiffJSONL(stdout, report)
	case "table":
		_, err := fmt.Fprintf(stdout, "equal=%v mode=%s meta=%d message=%d mismatches=%d\n",
			report.Equal,
			report.Mode,
			len(report.Meta),
			len(report.Message),
			len(report.Mismatches),
		)
		return err
	default:
		return fmt.Errorf("unknown format %q", format)
	}
}

func renderDiffJSONL(stdout io.Writer, report transfer.VerifyReport) error {
	enc := json.NewEncoder(stdout)
	if err := enc.Encode(diffJSONLSummary{Type: "summary", Equal: report.Equal, Mode: report.Mode}); err != nil {
		return err
	}
	for _, item := range report.Meta {
		if err := enc.Encode(diffJSONLDataset{Type: "meta", Report: item}); err != nil {
			return err
		}
	}
	for _, item := range report.Message {
		if err := enc.Encode(diffJSONLDataset{Type: "message", Report: item}); err != nil {
			return err
		}
	}
	for _, item := range report.Mismatches {
		if err := enc.Encode(diffJSONLMismatch{Type: "mismatch", Mismatch: item}); err != nil {
			return err
		}
	}
	return nil
}

type diffJSONLSummary struct {
	Type  string              `json:"type"`
	Equal bool                `json:"equal"`
	Mode  transfer.VerifyMode `json:"mode"`
}

type diffJSONLDataset struct {
	Type   string                       `json:"type"`
	Report transfer.VerifyDatasetReport `json:"report"`
}

type diffJSONLMismatch struct {
	Type     string                  `json:"type"`
	Mismatch transfer.VerifyMismatch `json:"mismatch"`
}

func diffTransferExitCode(err error) int {
	if errors.Is(err, transfer.ErrInvalidBundle) || errors.Is(err, transfer.ErrValidation) {
		return exitQuery
	}
	return exitInternal
}
