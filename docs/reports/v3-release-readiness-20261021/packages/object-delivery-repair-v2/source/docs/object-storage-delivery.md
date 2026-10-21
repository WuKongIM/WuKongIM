# Offline object delivery preparation

`scripts/prepare-object-storage-delivery.py` consumes the existing v1 preview
or v2 composite transport inputs. It recomputes the original planner output and
requires its exact canonical SHA-256. It preserves that schema, bytes and digest;
it never upgrades a preview plan to composite or invents a stable descriptor.
The original source, candidate, seal and activation-control identities remain
separate. Saved JSON verification fields are not production authorization.

The command performs local file operations only. There is no cloud SDK,
credential discovery, API endpoint loader, signer, production mode or workflow
connection. Stable remains disabled. Existing producer/control files and Pages
publication are unchanged.

## Prepare a new local directory

Provide a resource intent with exactly these fields. Actual cloud values are
unknown in this slice; non-null account/bucket/region/endpoint fields are rejected.

```json
{"production_enabled":false,"account_id":null,"bucket":null,"region":null,"endpoint":null,"domain":"packages.githubim.com"}
```

```sh
python3 scripts/prepare-object-storage-delivery.py \
  --inputs /path/to/reviewed-transport-inputs.json \
  --expected-plan-sha256 <original-planner-output-sha256> \
  --resource-intent /path/to/disabled-resource-intent.json \
  --activation-control-sha <reviewed-current-control-sha> \
  --max-routes 1000 --output-dir /path/to/new-delivery-directory
```

For composite inputs, add `--composite`; explicitly TEST ONLY composite fixtures
also require `--test-only`. The original planner still enforces its input and
signature-verification boundaries. The optional activation-control SHA is a
separate caller-supplied anchor, not authenticated GitHub truth; omission stays
null for preparation. Simulation requires a matching independent fake current
control observation before any PUT and again immediately before activation.
The tool never accesses private keys, Keychain or NAS.

The output directory must be new and outside the input roots. It contains:

- `transport-plan.json`: the original canonical planner bytes.
- `archives/<audit-id>/`: each bounded archive safely extracted once. Alias
  entries refer to the exact source audit ID and member, even when their public
  URL names a different generation. There is no extra archive extraction per
  alias and no signed-byte rewriting.
- `generated/<sha256>.txt`: the existing planner's exact UTF-8 mirrorlist bytes.
- `delivery.json`: input/plan digests, separate identity anchors, object facts
  and source paths, route request table, and existing client-verifier requests.

Object keys must agree on SHA-256, size and cache metadata. Entrypoints only
reference those physical objects. Inputs are revalidated after materialization.
The existing archive limits apply; each evidence JSON is bounded to 8 MiB.
Do not publish or archive this whole working directory as test evidence: it can
contain large packages. Keep only reviewed small JSON/log summaries.

## Route request table

The table uses exact, ordered URI matches: entrypoints, immutable aliases, then
original snapshot objects. `/` maps to current preview's `/index.html` object;
`/repo` remains preview. Aliases such as `/snapshots/stable/B/.../A.rpm` explicitly
map to their `public/snapshots/...` object, while original snapshot members keep
their own object key. No universal `/public/` prepend, URI normalization,
percent decoding, prefix fallback or recursive rewriting is performed.

The original planner can expose identical bytes under one URI from two physical
snapshots. The first matching entry is retained only when size, digest and cache
metadata agree. This does not permit conflicting payloads. Floating entrypoints
retain `no-store`; immutable aliases retain the original immutable cache value.

`--max-routes` bounds this exact request table (maximum 10,000), not Alibaba CDN
wire rules. This slice does not merge rules or assert provider expressibility.
A future provider adapter must compile equivalent routing, prove every listed
and neighboring URI's behavior, and enforce the provider's actual rule limit.
No cloud API parameters, ConfigIds or atomic-switch semantics are invented here.

## Explicit in-memory simulation seam

`simulate_delivery` accepts the same preparation arguments plus `api`,
`client_verifier`, `expected_configuration_sha256` and an optional
`allow_managed_removals=False`. It always prepares afresh: there is no success
journal to resume from. The CLI never loads or invokes these callbacks.

The test API declares `simulation_only=True` and implements `preflight`, `read_activation_control`,
`put_create_only`, `get_object`, `read_configuration`, `apply_configuration`,
`refresh`, and `check_public`. Their exact in-memory shapes are illustrated in
`tests/test_object_storage_delivery.py`; they are not Alibaba wire schemas.
An observation must explicitly be simulated, bind the disabled resource intent,
and model fresh, never-enabled bucket versioning. No IAM privilege is added to
make that observation: actual version-state reading remains provider work.
`read_activation_control` takes no expected SHA and reads separate fake current
state. Its observation must contain exactly `simulated: true` and a 40-character
lowercase hexadecimal `activation_control_sha` matching the caller's anchor.
Both observations are preserved in `activation_control_observations`; missing,
unknown or changed current control stops the simulation without a new activation.
Historical source/candidate/seal controls cannot substitute for this observation.
Real GitHub current-control resolution remains outside this offline slice.

Each PUT is create-only. An existing-object conflict or unknown timeout is
resolved only by GET of actual bytes, size and cache metadata. ETag, HEAD and old
receipts cannot substitute. All objects must be read back before activation.
The simulation then rechecks original inputs and the complete current config.
The bounded config diff includes additions, replacements and removals of managed
routes; removals need explicit simulation opt-in. Unmanaged configuration is
rejected. An observed stable configuration cannot be replaced by preview-only.

After applying the complete route set, configuration is read back even if the
apply callback raises. The simulation records `write_attempted`, observed before
and after state, or an unknown observation, without claiming rollback. Refresh,
origin/public-entrypoint checks and clients follow; a failure leaves the receipt
`incomplete` at its stage and preserves observed effects. It neither deletes
objects nor promises atomic CDN switching.

The client callback receives `channel`, exact source/version and audit/snapshot
anchors, and `kwargs` for the existing `validate_clients` interface in
`validate-production-package-clients.py`. Preview uses its original snapshot
interface; stable uses transport inputs and requires installed CLI acceptance.
No signer, native downloader or installed CLI is reimplemented or executed here.
The outer checks require both existing APT and both RPM distributions, exact
target downloads and matching installed identities. Original client receipts,
including stable's `transport_inputs_sha256` and `transport_plan_sha256`, must
bind this preparation's exact input bytes and original planner digest. A receipt
for the same stable candidate in a different composite is insufficient. Receipts,
including their false provenance/publication fields, are retained unchanged and
hashed. These are fake callback results in this slice, never public verification.

`simulation.json` can report only `simulated_complete` or `incomplete`. Its
production/publication/cloud/provenance flags stay false. Preparation errors
before any API call raise without a simulation receipt; later API failures leave
stage evidence. This is a test seam, not a sandbox for untrusted callback code.

## Validation and remaining provider work

```sh
python3 -m unittest discover -s tests -p 'test_object_storage_delivery.py' -v
```

Tests use existing real archive fixtures and in-memory APIs. The existing
composite structural fixture substitutes native verification; cryptographic
coverage remains in the existing Linux native drill. These tests do not rerun
that drill, access GitHub or demonstrate a real upload, origin or client result.

The remaining original D work is a reviewed provider adapter and explicit
object-storage publication control: actual resource identities and permissions,
fresh GitHub/control verification, create-only OSS writes and remote readback,
equivalent CDN wire configuration/readback, public origin checks and execution
of the existing four clients. Those operations still require their actual
resource and publication authorization. No production enablement can be inferred
from local plans, fixture successes or JSON fields.
