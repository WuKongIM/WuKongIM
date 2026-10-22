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
wire rules. Preparation does not merge rules. The optional SDK compiler below
can represent some larger finite tables without changing them; live provider
equivalence and neighboring URI behavior still require separate acceptance.
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


## Optional official SDK library contracts

`aliyun-object-storage-provider.py` and `verify-object-storage-delivery-inputs.py`
are importable libraries, with no production command or publisher integration.
The original planner, materializer, simulation, signed producers and four-client
validator remain unchanged. SDK operation receipts always retain
`production_enabled=false`, `publication_verified=false`, `cloud_verified=false`
and `wire_equivalence_verified=false`. `activate()` unconditionally rejects.
No local JSON flag or saved journal supplies publication authority.

The optional lock pins OSS V2 1.4.1, official SDK Core 2.16.1, CDN 3.8.8 and STS
3.1.3, plus every transitive dependency and published artifact SHA-256. Install
with `python3 -m pip install --only-binary=:all: --require-hashes -r
requirements-object-storage-provider.lock`. The independent PR/main CI installs
this lock and requires a nonempty SDK suite with zero skips. Ordinary stdlib
tests do not import these dependencies. The SDK contracts mount an in-memory
HTTP adapter on an instance session, exercise real SDK signing/serialization and
response handling, and forbid provider sockets. They are not live acceptance.

The Core client's instance session avoids the newer Tea/Darabonba client's
global session state. SDK signing is unchanged. Credentials are explicit
temporary inputs, never discovered from environment, files or instance metadata.
Sessions ignore proxies and netrc, forbid redirects and content encoding, verify
TLS, bound control responses, and disable retries. SDK debug logging is rejected
before constructing SDK clients or entering credential-bearing SDK operations,
with an additional transport check. Core's logger is global: callers must keep
debug disabled throughout an operation, including concurrent code. The library
does not claim isolation from concurrent global logging reconfiguration.
The adapter does not mutate process environment or global SDK HTTP state.

Each create-only PUT first verifies the exact caller, private bucket owner and
never-enabled versioning. A bounded frozen copy prevents local file replacement
between hashing and upload. PUT uses the exact length, Cache-Control and
`x-oss-forbid-overwrite=true`; only `FileAlreadyExists` is an accepted conflict.
An unknown transport result triggers complete GET verification, never another
PUT. GET requires EOF, exact length, SHA-256 and cache metadata; it closes the
stream on every outcome. No raw SDK exception, authorization or token is a
public receipt. No resource creation, deletion or permission change API exists.

Finite rewrite compilation is a **static review candidate** only. It uses the
official `back_to_origin_url_rewrite` arguments `source_url`, `target_url`,
`flag=break`, and explicit `sequence`, with an absolute PCRE end anchor. It keeps
exact source alias targets, rejects unsafe paths/collisions, caps the entire
domain at 50 rules, and caps a request at 20 functions. A 1,024-byte local
capability bound is conservative, not a provider limit claim. After exact route
deduplication, at most 50 routes keep the original literal bytes and order.
Larger tables can group only three proven relations: identical snapshot paths
under `/snapshots/{preview,stable}/r<ID>-<64 lowercase hex>/`, aliases whose key
is exactly `public` plus the URI, or a URI suffix under one exact current
snapshot's `/site/` target prefix. Root, generated objects and unknown mappings
remain literal; arbitrary routes sharing a target are not coalesced.

Each group emits `^escaped_prefix(escaped_suffix1|escaped_suffix2|...)\z`
and its fixed target prefix plus `$1`. Every alternative is a complete listed
suffix, with one capture and no wildcard coverage. Lexicographic suffix chunks
fit the 1,024-byte expression bound; rules are ordered by their earliest original
route and retain explicit sequence. An oversized singleton or more than 50
resulting rules is rejected. Source URLs stay the first function argument and
the configuration identity. Literal-to-group changes, source removal or new
chunk boundaries that would delete an existing source still fail before writes.

The frozen TEST ONLY native A/B fixture retains all 178 routes, 153 objects and
original source/archive/receipt/control/input/plan anchors per generation.
Both maps compile to 17 rules, longest 1,017 bytes, with exact finite inverse
equality and official SDK HTTP serialization/readback tests. Their source sets
match and only two targets change, so this pair reuses its ConfigIds. Seventeen
is a result for these fixed groups, not a global minimum or a guarantee for
future inputs. These contracts use public data captured after actual native
verification; they neither repeat signatures nor upload all 153 objects.

Live PCRE behavior,
encoded URI/query normalization and actual parameter limits remain unverified,
so this compiler cannot authorize activation. The SDK configuration primitive
binds the complete observed body and its digest to the exact resource and domain,
then compares a fresh complete observation before any configuration write. It
records attempted batches and before/after digests, including partial or unknown
outcomes. It refuses unmanaged rewrites, deletion, unknown functions/groups or
observed drift; it neither implements a transaction nor guarantees atomic POP
changes. The complete raw configuration observation is available to its caller.

Refresh primitives accept exact canonical file URLs and query only returned task
IDs (at most ten per query); exact URL/type/ID and Complete status are required.
They do not prove public origin/cache/TLS readiness. RefreshTaskById requires
read permission on Resource `*`; do not misdescribe it as domain scoped.

Fresh input authentication recomputes the original v1/v2 plan, authenticates
historical controls, reads exact immutable audit/tag/assets, and runs the
existing source resolver plus actual attestation/metadata helpers. Historical
candidate control, historical seal control, executor control and product source
SHA remain distinct. V2 compares historical sealed asset IDs; v1 first anchors
IDs from fresh resolution because its descriptor contains only byte identities.
Call again with the previous observation before the next write phase; any
source/audit/current-control/input/plan drift rejects. A future reviewed writer
must bind this fence to its exact preparation/resource and must invoke the
unchanged four-client verifier. This slice does not supply that writer, channel
authority, production resources, origin/TLS checks or public acceptance.

Official references: [OSS V2](https://pypi.org/project/alibabacloud-oss-v2/1.4.1/),
[SDK Core](https://pypi.org/project/aliyun-python-sdk-core/2.16.1/),
[CDN SDK](https://pypi.org/project/aliyun-python-sdk-cdn/3.8.8/),
[STS SDK](https://pypi.org/project/aliyun-python-sdk-sts/3.1.3/), and
[CDN function contract](https://www.alibabacloud.com/help/en/cdn/developer-reference/parameters-for-configuring-features-for-domain-names).

## TEST ONLY native preview fixture seam

`tests/native_object_delivery_fixture_drill.py` constructs tiny data-only native
preview and stable A/B packages in the existing pinned signing image. It reuses
the trusted repository builder, family signer, composer, canonical archiver,
external audit receipt and default native verifier. Private TEST ONLY signing
material is destroyed before verification or public export. It then supplies the
real preview archive to the existing loopback stable sealing helper and exercises
the unchanged v2 planner and offline materializer, including exact object bytes,
cross-generation aliases, current client request identities and rejection cases.
No native verifier is substituted.

The separate `export_delivery_generations` helper cannot replace
`export_client_generations`: the latter still requires explicit fixed-source
Linux amd64 executable inputs. The fixture drill checks missing binaries,
malformed ELF inputs and a wrong source SHA are rejected. Format validation does
not independently prove arbitrary executable provenance. These data-only
packages contain no product executable, and no package is installed or run.

Preview source attestation JSON and loopback GitHub observations are synthetic
TEST ONLY byte anchors. Actual native signature verification does not authenticate
those source claims. Fresh production authentication must reject the composite
TEST ONLY inputs. The original false GitHub, cloud, activation and publication
fields remain false. This seam does not test official SDK delivery, live CDN
rewrite equivalence, production authority or public installed-CLI acceptance.

Run the drill as a non-root user in the image digest from
`manifests/signing-toolchain.json`, with networking disabled, a read-only checkout
and a public output directory. Supply `--trusted-root` containing the six exact
files from `manifests/trusted-toolchain.json`, `--source-sha` for the tested checkout,
and an initially empty `--output-dir`. Bound the container lifetime and remove it
after exit. `result.json` records every declared case, command outcomes, native
verification limits and cleanup; `fixture-export.json` binds the public preview
descriptor and A/B paths. A nonzero exit, omitted case, timeout or cleanup failure
is a failed run. The read-only native safety workflow runs these 29 cases in a
separate 15-minute, network-disabled container step with `--init` to reap orphaned
GPG agents, and checks both receipts. It
uploads only public JSON evidence. Existing native candidate 31 and four-client
49-case commands and deadlines remain separate and unchanged. The production
producers, validators, channels and keys are unchanged.

## Disabled production entrypoint and read-only HTTPS probe

`scripts/publish-object-storage-delivery.py` reserves a fixed `upload` or
`activate` command and reads only the executing checkout's
`manifests/object-storage-publication.json`. This phase accepts only its exact
`enabled: false` template, with every operation, target and resource binding
null. Both commands refuse before reading transport inputs or writing an output
directory. An enabled manifest is also refused: production composition is not
implemented. Changing a flag does not complete or authorize publication.

The callable `verify_fresh_inputs` delegates to the existing fresh validator,
including real clean Git, current main, historical control bytes, source
attestation/native helpers and immutable numeric audit identities. It exposes
no replacement verifier. Future upload and activate invocations must each run
fresh verification and preparation independently. Activate must GET every
planned object without PUT. `previous` can only bind the first observation of
that same activation, never a saved upload receipt from another control commit.
Original producer bytes, equality rules, client verification and Pages controls
are unchanged. The SDK composition and actual public-client execution remain
unconnected in this phase.

`probe_public` is a narrow read-only response verifier, not a publication command.
It GETs the canonical `https://packages.githubim.com` URI, optionally using an
explicit reviewed `*.kunlunaq.com` CNAME as the connection destination. Curl
`--connect-to` preserves canonical Host, SNI and certificate-name verification;
changing Host alone is insufficient. The exact path and query are preserved.
No redirects, proxy/environment CA overrides, insecure TLS, encoded body,
HEAD-only checks or ETag substitution are accepted. The probe streams a complete
body and requires exact size, SHA-256 and Cache-Control, with a 64 KiB header
bound, the original planner's total archive byte bound, and a 20-second deadline.
It terminates and reaps its child on every exit. Response observations retain
false production/publication/public-completion fields: one probe cannot prove
source provenance, CDN PCRE equivalence, all POP caches or four-client acceptance.

The explicit `probe_test_only` seam accepts only loopback and an owned fixture
CA certificate. It is unavailable through CLI flags and always labels its result
TEST ONLY. `tests/provider_sdk/test_object_storage_publication.py` runs actual
isolated temporary Git repositories and local TLS services, observes Host/SNI,
rejects wrong SAN/CA, redirects, incorrect/oversized responses and timeouts, and
checks child/listener cleanup. Temporary TLS keys are generated in owned scratch
and removed; production keys, Keychain and NAS are never accessed. These tests
are an explicit integration suite in the existing read-only provider workflow,
not part of default unit discovery. Its bounded JSON receipt is the only new
integration artifact; fixture keys, package trees and caches are not archived.
