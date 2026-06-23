package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// exportCommandFlags contains flags accepted after `wkdb export`.
type exportCommandFlags struct {
	// output points to the WKDB import bundle root directory to create.
	output string
	// overwrite allows replacing an existing output directory.
	overwrite bool
	// pageSize bounds inspect scan rows per export page.
	pageSize int
	// messageFileRows bounds message rows per exported message file.
	messageFileRows int
}

func runExport(ctx context.Context, global cliFlags, args []string, stdout, stderr io.Writer) int {
	exportFlags, code := parseExportCommandFlags(args, stderr)
	if code != exitOK {
		return code
	}
	if strings.TrimSpace(exportFlags.output) == "" {
		fmt.Fprintln(stderr, "export error: --output is required")
		return exitConfig
	}

	cfg, err := resolveCLIConfig(global, os.Environ())
	if err != nil {
		fmt.Fprintf(stderr, "config error: %v\n", err)
		return exitConfig
	}
	if cfg.options.HashSlotCount == 0 {
		fmt.Fprintln(stderr, "config error: export requires --hash-slot-count or WK_CLUSTER_HASH_SLOT_COUNT")
		return exitConfig
	}
	if cfg.options.MetaPath == "" || cfg.options.MessagePath == "" {
		fmt.Fprintln(stderr, "config error: export requires both metadata and message storage paths")
		return exitConfig
	}

	store, err := inspect.OpenStore(cfg.options)
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return exitConfig
	}
	stats, exportErr := transfer.ExportBundle(ctx, exportFlags.output, store, exportFlags.transferOptions(cfg.options.HashSlotCount))
	closeErr := store.Close()
	if exportErr != nil {
		fmt.Fprintf(stderr, "export bundle: %v\n", exportErr)
		return exportTransferExitCode(exportErr)
	}
	if closeErr != nil {
		fmt.Fprintf(stderr, "close store: %v\n", closeErr)
		return exitInternal
	}
	printExportSummary(stdout, stats)
	return exitOK
}

func parseExportCommandFlags(args []string, stderr io.Writer) (exportCommandFlags, int) {
	var flags exportCommandFlags
	fs := flag.NewFlagSet("wkdb export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&flags.output, "output", "", "path to write WKDB import bundle root")
	fs.BoolVar(&flags.overwrite, "overwrite", false, "replace an existing output directory")
	fs.IntVar(&flags.pageSize, "page-size", 0, "rows per read-only export scan page")
	fs.IntVar(&flags.messageFileRows, "message-file-rows", 0, "message rows per exported message file")
	if err := fs.Parse(args); err != nil {
		return exportCommandFlags{}, exitConfig
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "export error: unexpected argument %q\n", fs.Arg(0))
		return exportCommandFlags{}, exitConfig
	}
	if flags.pageSize < 0 || flags.messageFileRows < 0 {
		fmt.Fprintln(stderr, "export error: page sizes must be non-negative")
		return exportCommandFlags{}, exitConfig
	}
	return flags, exitOK
}

func (f exportCommandFlags) transferOptions(hashSlotCount uint16) transfer.ExportOptions {
	return transfer.ExportOptions{
		HashSlotCount:   hashSlotCount,
		PageSize:        f.pageSize,
		MessageFileRows: f.messageFileRows,
		Overwrite:       f.overwrite,
	}
}

func printExportSummary(stdout io.Writer, stats transfer.ExportStats) {
	fmt.Fprintf(stdout, "exported=%d messages=%d subscribers=%d channels=%d files=%d bytes=%d\n",
		stats.RowsExported,
		stats.MessagesExported,
		stats.SubscribersExported,
		stats.ChannelsExported,
		stats.FilesWritten,
		stats.BytesWritten,
	)
}

func exportTransferExitCode(err error) int {
	if errors.Is(err, transfer.ErrInvalidBundle) || errors.Is(err, transfer.ErrValidation) {
		return exitQuery
	}
	return exitInternal
}
