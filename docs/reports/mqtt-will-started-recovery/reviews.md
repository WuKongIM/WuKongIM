# Started Will recovery review

Scope: base `14386ca24a4eb00e7a4d4ebf5899023736e2c70a`, tracked working-copy diff
and relevant untracked files in `review-scope.json`. No earlier branch commits
are included. Reviewers read the applicable AGENTS/FLOW documents and approved
parent/spec contracts. Product changes were stable during the two reviews;
report receipts and FLOW formatting changed subsequently.

## Standards

One P2: Open capped combined entries at 1,032 but could delete more than the
documented eight staging files. Fixed by counting staging entries separately
and rejecting >8 before deleting. Supplemental review confirms resolution and
finds no actionable issue in the stronger exact-worker sleep/SIGKILL fixture.
No other actionable documented-standard violations or material Fowler smells.

## Spec

One P3: the same staging-file bound disagreed with the explicit recovery contract.
Fixed and confirmed in supplemental review. No unsafe redispatch found in this
slice: Reserved admission/sealing serialize durably; Admitted/missing evidence
cannot grant negative proof; exact tuple/generation lock, definite successor CAS,
fresh permission, frozen content and positive receipt precedence are preserved.

The broader parent requirement remains partial: Admitted-before-SEND crashes,
legacy/lost journals, unknown-effect terminal recovery, orphan cleanup and
aggregate shared-storage admission remain outstanding. Documentation and the
Changelog state those limits. Review is not whole-branch or full-failure acceptance;
the final six-case process rerun supplies only its recorded bounds.

Summary: initial findings 1 Standards / 1 Spec; both resolved; supplemental
findings 0 Standards / 0 Spec.

A final own-audit hardening removes conflict-path reservation deletion: a
same-node equal tuple can back an earlier unknown claim that applies late.
The Spec reviewer confirms conservative retention is correct and finds no new
issue. The delayed unknown-claim race itself is not qualified by this matrix;
final builds and race/process regression reflect the hardening.
