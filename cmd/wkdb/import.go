package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// importCommandFlags contains flags accepted after `wkdb import`.
type importCommandFlags struct {
	// input points to the WKDB import bundle root directory.
	input string
	// dryRun validates the bundle without opening a writable NodeStore.
	dryRun bool
	// requireEmpty requires the target node store to be empty before writing.
	requireEmpty bool
	// subscriberBatchSize bounds subscriber chunks passed to transfer.ImportBundle.
	subscriberBatchSize int
	// messageBatchSize bounds message chunks passed to transfer.ImportBundle.
	messageBatchSize int
	// messageBatchBytes bounds approximate message payload bytes per import chunk.
	messageBatchBytes int
}

func runImport(ctx context.Context, global cliFlags, args []string, stdout, stderr io.Writer) int {
	importFlags, code := parseImportCommandFlags(args, stderr)
	if code != exitOK {
		return code
	}
	if strings.TrimSpace(importFlags.input) == "" {
		fmt.Fprintln(stderr, "import error: --input is required")
		return exitConfig
	}

	env := os.Environ()
	if importFlags.dryRun {
		hashSlotCount, err := resolveCLIHashSlotCount(global, env)
		if err != nil {
			fmt.Fprintf(stderr, "config error: %v\n", err)
			return exitConfig
		}
		stats, err := transfer.ValidateBundle(ctx, importFlags.input, importFlags.transferOptions(hashSlotCount))
		if err != nil {
			fmt.Fprintf(stderr, "validate import bundle: %v\n", err)
			return importTransferExitCode(err)
		}
		printImportSummary(stdout, stats)
		return exitOK
	}

	if !importFlags.requireEmpty {
		fmt.Fprintln(stderr, "import error: --require-empty is required for a real import")
		return exitConfig
	}
	cfg, err := resolveCLIConfig(global, env)
	if err != nil {
		fmt.Fprintf(stderr, "config error: %v\n", err)
		return exitConfig
	}
	if cfg.nodeOptions.MetaPath == "" || cfg.nodeOptions.MessagePath == "" {
		fmt.Fprintln(stderr, "config error: import requires both metadata and message storage paths")
		return exitConfig
	}

	store, err := db.OpenNodeStore(cfg.nodeOptions)
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return exitConfig
	}
	stats, importErr := transfer.ImportBundle(ctx, importFlags.input, store, importFlags.transferOptions(cfg.options.HashSlotCount))
	closeErr := store.Close()
	if importErr != nil {
		fmt.Fprintf(stderr, "import bundle: %v\n", importErr)
		return importTransferExitCode(importErr)
	}
	if closeErr != nil {
		fmt.Fprintf(stderr, "close store: %v\n", closeErr)
		return exitInternal
	}
	printImportSummary(stdout, stats)
	return exitOK
}

func parseImportCommandFlags(args []string, stderr io.Writer) (importCommandFlags, int) {
	var flags importCommandFlags
	fs := flag.NewFlagSet("wkdb import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&flags.input, "input", "", "path to WKDB import bundle root")
	fs.BoolVar(&flags.dryRun, "dry-run", false, "validate the bundle without opening a writable store")
	fs.BoolVar(&flags.requireEmpty, "require-empty", false, "require an empty target store before importing")
	fs.IntVar(&flags.subscriberBatchSize, "subscriber-batch-size", 0, "subscriber rows per import batch")
	fs.IntVar(&flags.messageBatchSize, "message-batch-size", 0, "message rows per import batch")
	fs.IntVar(&flags.messageBatchBytes, "message-batch-bytes", 0, "approximate message payload bytes per import batch")
	if err := fs.Parse(args); err != nil {
		return importCommandFlags{}, exitConfig
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "import error: unexpected argument %q\n", fs.Arg(0))
		return importCommandFlags{}, exitConfig
	}
	if flags.subscriberBatchSize < 0 || flags.messageBatchSize < 0 || flags.messageBatchBytes < 0 {
		fmt.Fprintln(stderr, "import error: batch sizes must be non-negative")
		return importCommandFlags{}, exitConfig
	}
	return flags, exitOK
}

func (f importCommandFlags) transferOptions(hashSlotCount uint16) transfer.ImportOptions {
	return transfer.ImportOptions{
		HashSlotCount:       hashSlotCount,
		RequireEmpty:        f.requireEmpty,
		SubscriberBatchSize: f.subscriberBatchSize,
		MessageBatchSize:    f.messageBatchSize,
		MessageBatchBytes:   f.messageBatchBytes,
	}
}

func printImportSummary(stdout io.Writer, stats transfer.ImportStats) {
	fmt.Fprintf(stdout, "validated=%d written=%d messages=%d subscribers=%d channels=%d files=%d bytes=%d\n",
		stats.RowsValidated,
		stats.RowsWritten,
		stats.MessagesImported,
		stats.SubscribersImported,
		stats.ChannelsImported,
		stats.Files,
		stats.BytesRead,
	)
}

func importTransferExitCode(err error) int {
	if errors.Is(err, transfer.ErrInvalidBundle) || errors.Is(err, transfer.ErrValidation) {
		return exitQuery
	}
	return exitInternal
}
