# Go operator fixture repair — frozen before implementation

Observed Linux failure: run 39572139549, attempt 1, source 76a3ba23303e288345dbd37bc354b41d81b6f070 / merge 974545635cc9be14a6b5f00413eb4bdb56ae0b7b. Real shakeout production join completed with child status 0; the final fixture-only signaler wait produced -1/255. Original trace remains immutable in scripts-wait255-diagnosis/generation-1-observed-failure. Historical observer 255 has not independently been tied to this location.

Approved seam: the existing two process-level shell fixtures, extracting the real production functions unchanged. Go becomes the external operator. No product hooks, threshold changes, 255 normalization, new retry, or gate timeout change.

Failure modes / regression constraints:
1. Missing, malformed or wrong-parent ready record must fail; an early exit 0 before readiness is not success.
2. Ready record must bind the started shell and its live target in the owned PGID. TERM is sent once, after the existing 50 ms delay, and the shell must record the target was still alive at signal receipt.
3. Production join and original child-dead / status / PID-clear assertions remain. Nonzero original exit including 17/255 must remain visible.
4. Cancellation before/after ready, early exit and malformed readiness must kill only the newly created fixture PGID, then join the started shell. Recorded fixture child must not survive. External sentinel must survive.
5. Trace remains separate from assertion stdout and bounded to 64 KiB on failure. Bash 3 phase logging remains valid; Bash 5 xtrace only begins inside the test fixture.
6. Shipping repetition is three sequential attempts per fixture (first failure stops); each retains five seconds. Additional bounded local repetitions are validation, not green selection. Linux complete gate is still required.

Test-first: add helper failure-control process cases with a transitional CombinedOutput implementation, record the failures, implement owned Go operator, then replace only the two signaler fixtures and run both actual Bash versions. These controls test helper process supervision; they are not proof of the original Bash kernel/runtime cause.
