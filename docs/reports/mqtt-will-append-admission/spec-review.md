# Spec review

Scope: working copy against `68eb337c50267fb2e0114130ef2b028760d17555`,
`git diff HEAD`, no new commits, and the untracked source/spec files frozen in
`review-scope.json`. Reviewed the accepted operator scope and
`docs/specs/mqtt-will-append-admission.md`; applicable AGENTS/FLOW context was
read before package analysis. No process tests or external actions were run by
this reviewer.

Findings: **0**. No missing/partial requirement, unasked behavior, or apparently
implemented but incorrect behavior was identified in the frozen scope.

The version-1 Admitted decoder remains conservative. Version-2 admission and
irreversible AppendIssued permission compete with sealing under the same
generation-owned mutex, with exact identities and durability before granting
submission. Cancellation or uncertain sync cannot grant submission; an issued
record cannot become negative proof. The product callback survives DTO mapping
and cloning, runs before both origin submission branches, remains attached to
the original router item for retries, and is stripped from accepted work and
node RPC. Stale callbacks retain the original turn deadline and journal boot.

Positive receipt recovery still precedes current policy; sealed recovery
requires fresh policy, a reserved successor and a definite Slot CAS. Frozen
publication bodies and the committed identity path remain intact.

The added process scenarios cover both 256-Slot topologies, pre-send/router
cuts, issued unknown and version-1 migration. Existing committed replay and
accepted-append delay cases remain present. Runtime pass/fail evidence belongs
to the parent validation report; this static review does not certify it.
Full 1,024-record pressure, delayed CAS/cleanup races, partitions and terminal
unknown-effect recovery remain explicitly separate qualification work.

## Harness supplement

Reviewed the three frozen files in `review-supplement-scope.json`: **0 new
findings**, matching hashes, unchanged product Go code. Startup-enabled
publication counting remains observable immediately after restart. The
case-owned symlink swap changes only the next executed image; existing peers
keep running the legacy image. `StartStoppedNode` joins prior process-group
cleanup and reuses the exact node spec/data, satisfying the compatibility
scenario's intended upgrade boundary. The FLOW/index adjustment does not alter
semantics. The 14-case rerun is still in progress; no runtime success is inferred
from this static supplement.
