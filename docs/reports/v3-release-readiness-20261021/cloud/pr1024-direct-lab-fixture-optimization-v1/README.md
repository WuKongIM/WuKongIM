# Direct-lab Start fixture optimization — first RED/GREEN evidence

Base remains `44de9452d94edae16129b5781d794f8c1c6c7c57`, tree `b69a44af04f20143b690e7a3860cc45caa95a0f4`. Three source files are changed and frozen for review; no commit, push, full-suite run, CI run, cloud request, shared scheduler edit, protected policy/workflow edit or time-limit change occurred.

## Result

The Start acceptance fixture unnecessarily built `wkcloudgate` and `wkcloudbundle`, although its start scenario never calls either tool. Injecting the two existing command paths with a fail-if-called executable removes both builds while preserving every original assertion. The stub writes an unexpected operation to the original call log before exiting 96, so an accidental invocation fails the existing exact-order assertion even if an exit were ignored.

The real-process case moves from the default file to `chat_lifecycle_direct_lab_start_integration_test.go` because it executes Bash and SSH key generation. Its `repoRoot(t)` now admits it through the existing integration scheduler. This scheduling consequence is distinct from the two eliminated builds; no wall-time saving from the tier move is claimed. Other tests, product scripts, real binary-build coverage and the scheduler are unchanged.

The parent timeout diagnosis established varying phase spans and scheduling order; its root CONT/child event intervals and lock source are **consistent with admission/lock waiting**, with no acquisition timestamp. No CPU contention cause or whole-suite recovery is inferred here.

## Tests first, one fixed scratch seam

The prewritten `plan-and-failure-matrix.md` lists every failure mode and the three allowed source files. `before-freeze.json` binds the original source and single scratch test source. `fixture_test.go` contains the entire original function and all its assertions, plus diagnostic recording. Both A and B run this same serial source and command; only `DIRECT_LAB_INJECT_UNUSED_TOOLS` changes the two existing tool inputs. The same Go tracer is present in both.

| First run | Existing assertions | Actual fixture Go build calls | Diagnostic result | Fixture observed seconds |
|---|---|---:|---|---:|
| `baseline-first` | all passed | 2, both successful | RED: unnecessary calls remain | 2.458232 |
| `variant-first` | all passed | 0 | GREEN | 0.569078 |

The baseline actual build spans were 0.498377 seconds (`wkcloudgate`) and 0.290890 seconds (`wkcloudbundle`). The 1.889154-second whole-fixture difference includes other per-run variation; it must not all be causally assigned to builds. This is one local A/B with existing local caches, no prewarming or cache manipulation. It establishes eliminated work, not a Linux saving percentage or a guarantee to complete the original 600-second gate. No source change was made until this comparison passed.

`semantic-comparison.json` proves:

- The exact eight-operation trace is unchanged: build → materialize → quote → selector-from-plan → release-enable → acquire → release-enable-after-provision → selector. No unused-tool marker appears.
- All request artifact names, file/directory types and private permissions match.
- All generated request JSON objects match after normalizing only the predeclared `state.json.created_at` and `state.json.activated_at` wall-clock fields.
- The deliberate inventory change is the absence of `tools/wkcloudgate` and `tools/wkcloudbundle`; `tools/timeout` remains. That difference is explicit, not normalized away.
- Private and public SSH key bytes and built binaries were never copied to this evidence directory. Tests checked only key presence/private modes; artifact JSON includes the original fixture's nonfunctional literal `ssh-ed25519 fake`, not generated SSH material. Original temporary directories were cleaned by Go.

## Source equivalence and related first validation

`validation-summary.json` checks that the original default file is byte-identical after removing exactly this 108-line function. Removing only the declared stub block and two environment lines from the new function reproduces the original function byte-for-byte. No old assertion was deleted or weakened.

- Default listing: exact target absent (0 entries).
- Integration listing: exact target present once.
- First related default focused run: 11 top-level PASS, no failure or skip; target absent as intended.
- First related integration focused run: 15 top-level PASS, no failure or skip; target executes and passes exactly once.
- All six scratch/list/focused process-group receipts prove zero remaining processes. No watchdog expired.
- `gofmt` and `git diff --check` passed. No additional tests were written after implementation.

Commands and exact argv/environment are in each run's `command.json`. These local focused commands are diagnostic/regression evidence, not a filtered substitute for the protected named check. Full unchanged Linux acceptance remains required on the next independently reviewed head. The existing PR already carries Changelog entries for its user-visible fixes; this additional change alters test execution only and adds no product release-note claim.

## Files and repeatability

- `review-source-freeze.json`: fixed hashes for the three changed files, unchanged rule/script/scheduler hashes, base HEAD/tree.
- `final.diff`: all three source changes, including the new integration file.
- `plan-and-failure-matrix.md`, original function, scratch source, Go trace shim, runner, baseline/variant artifacts and their first outputs retain the first RED/GREEN chain.
- `default-*` and `integration-*`: first listing/focused stdout, safe event projection, exact command and terminal cleanup receipt.

To repeat the scratch experiment without overwriting first results, copy only `fixture_test.go`, `go-trace.py` and `run_scratch.py` into a fresh empty diagnostic directory, verify their hashes against the allowlist, verify the retained `scripts/chat-lifecycle/direct-lab.sh` hash against the freeze, then run `python3 run_scratch.py baseline-first` followed by `python3 run_scratch.py variant-first`. The runner rejects existing output directories. Each run has a 90-second diagnostic group watchdog and a 70-second Go alarm; these do not change repository policy. `prepare_scratch.py` records how the fixture was originally derived from the frozen pre-change source; do not run it against the moved post-change file.

The evidence is finite and allowlisted; no whole temporary request directory, raw key, compiled binary or cloud credential is included.
