# First observed d049073 native failure; tests before diagnostic change

Run39564810498 attempt1 job119104436724 fails in native local signing after APT
wrong-passphrase/full-primary negative cases. Original run/job/log/artifact are
retained unchanged. Signer stderr and exact failing case are discarded by run();
this prevents identifying the native cause. Current evidence bounds failure to
RPM positive invocation(s), not a specific RPM tool stage.

Narrow repair: keep exact failed case/family/input mode/exit and bounded sanitized
existing top-level signer diagnostic in the existing public TEST ONLY JSON. Do not
change signer/validator, thresholds, negative expectations, fixture keys or workflow.

Failure cases before implementation:
- Positive signer rejection loses exact case and top-level stage.
- Recognized diagnostic accidentally echoes fixture password/base64: redact exact values.
- Unexpected/binary/noisy child stderr becomes an unbounded or secret-bearing public log:
  allow only the bounded known top-level signer line, otherwise use a constant fallback.
- Nonzero negative cases lose their unchanged no-stdout/no-output/no-stage requirements.
- Top-level failure never reaches always-upload JSON: retain passed:false and failure.

Only short pure-mock Python tests are planned locally; no native command/GPG/Docker
or load is executed while quiet remains. Native cause still requires a subsequent
root-owned exact-source CI observation. Do not label this diagnostic patch a native fix.

## Follow-up canonical fixture defect (before fixture repair)

Parent source inspection found prepare_inputs writes ordinary json.dumps output;
the production signer requires sorted compact JSON plus a trailing newline.
The native stderr was lost, so independently reproduce at the real loader boundary:
call actual prepare_inputs with mocked APT native-output capture and data-only
placeholder package bytes, then actual load_package_allowlist/load_active_allowlist.
Cover new, signed, active lists and exact package size/hash/path. Keep the real
signer canonical-encoding rejection unchanged. No GPG or native executable needed.
