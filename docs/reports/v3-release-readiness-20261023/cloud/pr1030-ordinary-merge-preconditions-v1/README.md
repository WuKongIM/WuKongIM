# PR1030 ordinary merge preconditions (read only)

Current head56c28749/base27fa6d63 exactly match sealed first-attempt CI, and main still equals base. GitHub reports OPEN, Draft=true, MERGEABLE, mergeStateStatus=CLEAN, reviewDecision empty, no formal Reviews. CI rollup is16success/2eventskip.

Current actor tangtaoit has repository admin/maintain/push. Main is protected: strict up-to-date checking=true, required status contexts/checks empty, required approval count0, enforce_admins=false; force pushes/deletions remain disabled. Repository/inherited rulesets list is empty, with a complete bounded response. Both Review Agent workflows are disabled_manually and byte-equal to their sealed CI metadata cut.

README says every PR defaults to human handling (lines733-734), and administrators retain manual merge authority whether or not Review Agent ran (810-811). Combined with the direct human standing instruction permitting future merges without another confirmation, the applicable path is root's ordinary exact-head GitHub merge. No Review Agent wake-up, workflow enable or protection bypass is needed or performed here.

The current concrete technical gap is Draft status; root must mark ready before merge. This snapshot does not certify root's independent Standards/Spec decisions. It supplies no release, loss-repair or cloud-start authority. The previously failed full-distribution run remains retained.

All eight external calls were GET/read-only. invocations.json binds exact arguments, result codes, byte lengths and hashes. No ready/merge/rerun/dispatch/cancel/settings mutation occurred. assessment.json carries exact permission/protection facts and references the separately sealed CI manifest.
