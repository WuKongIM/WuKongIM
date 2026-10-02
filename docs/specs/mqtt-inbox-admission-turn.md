# Bounded future-person inbox admission

This usecase advances the durable admission checkpoint for one person channel.
It reuses the native directory projector: a pinned Channel Ready marker for the
current runtime directory generation proves the native projection has completed
both UID registrations. It never uses Pending as that barrier. Only after that
read may it scan either UID's inbox qualifications and prepare person sources.
One turn visits one phase/page (at most 64 candidates, default 8), commits each
completed candidate separately, and may advance one participant when the page
ends. Progress survives failures and does not need a live connection owner.

This is preparation evidence, not an append capability. Product integration must
bind successful preparation to the same incarnation used by first persistent
append; initial inbox subscription discovery and cleanup remain separate work.

Failure inventory at the approved usecase and app-composition seams:

- Missing runtime/Channel, Pending directory or another directory generation
  must do no qualification scans, preparation or checkpoint writes.
- The pinned directory marker must not mix live Channel cache state with an
  older runtime snapshot; Channel deletion/recreation must force fresh progress.
- Qualification pages must be bounded, ordered, identity-valid and closed; short
  unfinished, duplicate, foreign, regressed or mixed-kind replies are not evidence.
- Each source preparation must finish before advancing its candidate. A failed
  candidate leaves earlier progress committed; a lost checkpoint reply resumes
  from current durable state instead of preparing the entire first page again.
- Directory/runtime incarnation changes and concurrent checkpoint writers must
  prevent stale completion. A stale post-commit read cannot acknowledge progress.
- Cancellation, deadlines, callback panic, invalid receipts and clock regression
  must return no Ready result. Readiness grants no Session/socket activation.
- Real single-node cluster validation uses the native directory projector and
  real protection/cursor/accounting paths for an offline Session before its first
  native person message. Explicit subscription qualification remains a fixture;
  the automatic message append hook and complete product listener are not claimed.

The candidate reader preserves read-kind-11 pagination: terminal pages retain
the input cursor; nonterminal pages return the last emitted key. Its pinned
index/primary scan inspects at most limit+1 witnesses. Missing primaries,
malformed keys and obsolete index entries return errors, including lookahead;
they cannot be silently removed from a successful admission page. This tightens
error handling without changing durable encodings or healthy page semantics.

A prepared source may retain the intent revision that originally established
its boundary after its subscription becomes Active or changes options. The
source preparer revalidates current intent and returns a lifetime-bound result;
admission verifies source/cursor identity and progress, without requiring the
historical binding's intent revision to equal a later qualification revision.

One call owns a single deadline, default five seconds (at most one minute).
Default page size is eight, configurable from one through 64. It uses no worker,
connection scope, global lock or volatile resume cache. Errors clear the returned
progress while preserving already committed checkpoints. Repeated Ready reads
perform no source work or checkpoint writes. A final pinned read verifies the
committed revision, phase/cursor progression and current directory incarnation.

Validated by deterministic storage/usecase tests and a real 256-hash-Slot
single-node cluster integration. The integration invokes the admission usecase
explicitly before native SEND; it does not replace the required automatic append
hook, concurrent append-incarnation fence, initial inbox projection, offline
maintenance scheduling or full product process E2E acceptance.
