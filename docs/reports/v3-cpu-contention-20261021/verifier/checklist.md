# CPU-placement artifact integrity checklist

Frozen run: 39467558163, attempt 1; harness `07ed85a22bdd2ebf75b776a92e71f21c02d2623f`; product `ef798352c40a94e57f768f76b2f4713712f827fe`; product bytes `8c3c79f2853b76be7f83dbd606404bf6ab1fdf34ce9370e82c056b389d03c57d`; profile bytes `1d0fb74bf51a6e747b56d1b43fece4670b40143a69a1601cc2f4be6f784a37a1`.

- The run JSON binds the exact run, attempt, harness and workflow. Workflow success alone is not a numeric verdict.
- Both product and driver files are hashed, inspected as Linux AMD64 ELF data, and never executed. Product build metadata must attest clean C and Go 1.25.11; driver bytes match its separate H receipt. The retained profile must exactly match the sealed C/H profile.
- The plan and all retained block IDs must be S/O/D/D/O/S. All six reports, including numeric rejects, remain present and bind the same H/C/binary/profile. No release gate directory or qualification flag is accepted.
- Check preparation age, release/application order, four-process identity, all initial/applied TID masks and unchanged guest topology. Placement is logical-CPU eligibility, not dedicated physical capacity.
- Every original window has valid duration/coverage, continuous points and actual masks for each owned process and each observed TID. Confirmed thread exits remain a coverage limitation; they must not be converted into zero waits or a complete all-thread wait total.
- Three Linux preflight test names each have one explicit pass and no skip/fail; the package test completion also passes.
- Every command receipt has successful exit and session cleanup, no timeout or retained processes. Exactly one product build and one driver build occur. Block test count stays one, timeout 12m, and no retries/order substitutions appear in events.
- Independently recalculate each phase's existing offered-rate/completion/P99/error/drop rules and allocated-byte ceilings. A numeric rejection is retained and reported separately from collection integrity. Unexpected errors or runtime/membership mutations invalidate collection.
- Hash the complete artifact tree and separate preflight logs. Incomplete artifacts produce an incomplete/invalid result with missing evidence preserved, never a passing surrogate.

Run only after download. `verify_artifact.py --artifact-root <conversation-cpu-contention> --preflight-dir <its parent> --run-json <exact run JSON> --output-dir <new outside-artifact directory>`. The output directory must not exist. The script reads files only, performs no network call, starts no product process and executes no artifact code. It uses only Python's standard library. A nonzero exit still writes `verification.json` and file digests.
