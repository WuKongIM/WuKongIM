# Documentation Alibaba Cloud CDN cutover

This runbook moves the public documentation edge to Alibaba Cloud CDN while
keeping GitHub Pages as the only content origin. It records a reviewed future
state; committing the repository support does not provision Alibaba Cloud,
change DNS, or change the GitHub Pages custom domain.

The integration is fail-closed and disabled by default. Keep repository
Variable `DOCS_CDN_ENABLED` absent or different from the exact string `true`
until every external prerequisite and the rollback snapshot below is complete.
With the integration disabled, `docs-pages.yml` continues publishing directly
to GitHub Pages; CDN refresh and certificate mutation are skipped.

## Accepted service boundary

```text
reader
  -> https://docs.githubim.com
  -> Alibaba Cloud CDN (global acceleration)
  -> HTTPS origin: origin-docs.githubim.com
  -> GitHub Pages
```

- `docs.githubim.com` is the only canonical reader-facing domain.
- `origin-docs.githubim.com` is the GitHub Pages custom domain and the CDN
  origin address, Host header, and TLS SNI after cutover.
- GitHub Pages remains the only source of documentation content.
- The goal is stable warm-cache performance in mainland China. A long-tail
  cache miss still crosses the international origin path and has no hard
  latency service-level objective.
- Alibaba Cloud CDN is the only public edge. A provider-wide CDN failure does
  not have a minute-scale recovery promise; publish the origin URL as the
  emergency direct path while the public domain is recovered.
- Initial billing is traffic-based. Configure alerts before cutover; establish
  hard caps only after seven days of representative traffic.

## Independent trust and certificate boundaries

| Boundary | Owner | Credential and certificate rule |
| --- | --- | --- |
| Content publication | GitHub Pages, Environment `github-pages` | Existing job-scoped GitHub OIDC; no Alibaba Cloud credential |
| CDN refresh | Alibaba Cloud, Environment `docs-cdn` | Short-lived OIDC session through a refresh-only RAM role; no Secret |
| Public edge TLS | Alibaba Cloud, Environment `docs-cdn-certificate` | Let's Encrypt DNS-01 plus a distinct certificate-rotation RAM role |
| Origin TLS | GitHub Pages | GitHub issues and renews the `origin-docs.githubim.com` certificate |

The refresh session lasts 15 minutes; the certificate-rotation session lasts
one hour so the bounded 20-minute DNS-01 issuance and CDN propagation checks do
not outlive their credentials. Both expire automatically shortly after their
owning job's maximum runtime.

Never store an Alibaba Cloud AccessKey in the repository. Constrain each RAM
role's trust to this repository, protected `main`, its exact Workflow, and its
Environment. Constrain the refresh policy to its one CDN refresh operation and
the certificate policy to the delegated ACME validation zone plus certificate
read/update operations for `docs.githubim.com`. Keep the two roles separate
even if Alibaba Cloud requires some policy resources to be expressed broadly;
the Workflows also reject any configured public domain other than
`docs.githubim.com`.

The Let's Encrypt account bundle is not an Alibaba Cloud credential. Store it
only as the `docs-cdn-certificate` Environment Secret described below. The
certificate private key is generated on the ephemeral runner, sent to the CDN
certificate API, and not committed or retained as an Artifact.

## Repository configuration contract

Create these Environments without changing the existing `github-pages`
Environment:

- `docs-cdn`: trusted protected-`main` deployments, no Secrets;
- `docs-cdn-certificate`: trusted protected-`main` deployments, with the ACME
  account Secret and no human reviewer on scheduled renewal.

Configure the following repository Variables only after their external targets
exist. Keeping both non-secret role ARNs at repository scope lets each
credential-free preflight enforce that the roles are distinct and belong to
the OIDC provider's account. Values shown in angle brackets are placeholders,
not provider state supplied by this repository.

| Variable | Required value or purpose |
| --- | --- |
| `DOCS_CDN_ENABLED` | Exact `true` only after preflight; absent or any other value keeps Alibaba jobs inert |
| `DOCS_CDN_DOMAIN` | Exact `docs.githubim.com` |
| `DOCS_CDN_CNAME` | Exact `docs.githubim.com.w.kunlunaq.com`, the Alibaba-assigned CDN CNAME without a trailing dot |
| `DOCS_CDN_PUBLIC_ROUTE_MODE` | Strict enum: `github-pages-precutover` while public DNS points directly to GitHub Pages; `alibaba-cdn` only after the CDN CNAME has propagated |
| `DOCS_CDN_OIDC_PROVIDER_ARN` | `<ALIBABA_GITHUB_OIDC_PROVIDER_ARN>` |
| `DOCS_CDN_OIDC_AUDIENCE` | `<AUDIENCE_ALLOWED_BY_THE_OIDC_PROVIDER>` |
| `DOCS_CDN_REFRESH_ROLE_ARN` | `<REFRESH_ONLY_RAM_ROLE_ARN>` |
| `DOCS_CDN_CERTIFICATE_ROLE_ARN` | `<CERTIFICATE_AND_ACME_RAM_ROLE_ARN>` |
| `DOCS_ACME_EMAIL` | `<OPERATED_ACME_CONTACT_ADDRESS>` |

Configure exactly one Environment Secret in `docs-cdn-certificate`:

| Secret | Purpose |
| --- | --- |
| `DOCS_ACME_ACCOUNT_BUNDLE_B64` | Base64-encoded ACME account bundle expected by `docs-cdn-certificate.yml`; it has no Alibaba Cloud authority |

Treat missing, partial, placeholder, or mismatched settings as not configured.
Do not set `DOCS_CDN_ENABLED=true` to discover what is missing in production.
The route mode is an operator assertion that the Workflow verifies against
direct public CNAME answers; it is not an instruction for the Workflow to
change DNS.

### Fixed OIDC and RAM boundary

This repository already uses the custom GitHub OIDC subject template
`repo + context + job_workflow_ref`. Configure the Alibaba Cloud role trusts to
accept only their corresponding exact subject and the configured audience:

```text
repo:WuKongIM/WuKongIM:environment:docs-cdn:job_workflow_ref:WuKongIM/WuKongIM/.github/workflows/docs-pages.yml@refs/heads/main
repo:WuKongIM/WuKongIM:environment:docs-cdn-certificate:job_workflow_ref:WuKongIM/WuKongIM/.github/workflows/docs-cdn-certificate.yml@refs/heads/main
```

Do not use a repository-wide, branch-only, or wildcard workflow subject. The
provider ARN and both role ARNs must belong to the same Alibaba Cloud account.

The refresh role needs exactly this data-plane policy after replacing
`<ACCOUNT_ID>`:

```json
{
  "Version": "1",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "cdn:RefreshObjectCaches",
      "Resource": "acs:cdn:*:<ACCOUNT_ID>:domain/docs.githubim.com"
    }
  ]
}
```

The certificate-rotation role needs the following operations. The
`alidns:DescribeDomains` list call is the one unavoidable account-wide read
used by the pinned lego provider to find the authoritative child zone; record
reads and mutations remain scoped to `acme.docs.githubim.com`.

```json
{
  "Version": "1",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "cdn:DescribeDomainCertificateInfo",
        "cdn:SetCdnDomainSSLCertificate"
      ],
      "Resource": "acs:cdn:*:<ACCOUNT_ID>:domain/docs.githubim.com"
    },
    {
      "Effect": "Allow",
      "Action": "alidns:DescribeDomains",
      "Resource": "*"
    },
    {
      "Effect": "Allow",
      "Action": [
        "alidns:DescribeDomainRecords",
        "alidns:AddDomainRecord",
        "alidns:DeleteDomainRecord"
      ],
      "Resource": "acs:alidns::<ACCOUNT_ID>:domain/acme.docs.githubim.com"
    }
  ]
}
```

Do not grant `cdn:AddCdnDomain`, `cdn:ModifyCdnDomain`,
`cdn:BatchSetCdnDomainConfig`, `cdn:PushObjectCache`, or DNS authority over
`githubim.com` to either Workflow role.

### Initialize the encrypted ACME account identity

Run this once from a trusted Linux or macOS workstation after reviewing the
checked-out scripts. `gh` must be authenticated as a repository administrator,
Go must be able to use toolchain `1.25.11`, and
`DOCS_ACME_EMAIL` must equal the repository Variable that the Workflow will
use. The command creates a Let's Encrypt account but requests no certificate
and touches no Alibaba Cloud resource. It sends the bundle directly to the
Environment Secret without printing its private key and deletes the temporary
state on exit. The helper fixes the production ACME directory and an EC P-256
account key, and accepts only the exact reviewed terms-of-service URL shown
below. It only creates or looks up the account and never sends an ACME
`newOrder` request.

```bash
set -euo pipefail
umask 077
: "${DOCS_ACME_EMAIL:?export DOCS_ACME_EMAIL first}"
repo_root="$(git rev-parse --show-toplevel)"
bootstrap_dir="$(mktemp -d)"
cleanup() {
  [[ -n "${bootstrap_dir:-}" && -d "$bootstrap_dir" && "$bootstrap_dir" != / ]] || return
  rm -rf -- "$bootstrap_dir"
}
trap cleanup EXIT
mkdir -p "$bootstrap_dir/helper-cache" "$bootstrap_dir/helper-tmp"
(
  cd "$repo_root"
  GOTOOLCHAIN=go1.25.11 GOWORK=off \
    GOCACHE="$bootstrap_dir/helper-cache" GOTMPDIR="$bootstrap_dir/helper-tmp" \
    GOENV=off GOTELEMETRY=off go build -trimpath \
    -o "$bootstrap_dir/certificate-helper" ./scripts/docs-cdn/certificate-helper
)
"$bootstrap_dir/certificate-helper" register-account \
  --email "$DOCS_ACME_EMAIL" \
  --state "$bootstrap_dir/state" \
  --accept-terms-of-service \
  "https://letsencrypt.org/documents/LE-SA-v1.8-July-06-2026.pdf"
"$bootstrap_dir/certificate-helper" pack-account \
  --email "$DOCS_ACME_EMAIL" \
  --state "$bootstrap_dir/state" \
  --output "$bootstrap_dir/account.bundle.b64"
gh secret set DOCS_ACME_ACCOUNT_BUNDLE_B64 \
  --repo WuKongIM/WuKongIM \
  --env docs-cdn-certificate \
  <"$bootstrap_dir/account.bundle.b64"
```

The decoded bundle schema is fixed to
`wukongim/docs-acme-account-bundle/v1` and contains the production Let's
Encrypt server, exact operator email, validated lego-compatible account JSON,
and the ACME account key. It never contains a CDN certificate or TLS private
key. Base64 is only transport encoding, so the bundle must exist only in the
encrypted Environment Secret and the protected temporary directory above.
The top-level bundle email and the local lego-compatible account email are
both fixed to `DOCS_ACME_EMAIL`. The ACME server may omit
`registration.body.contact`; when it returns that field, it must exactly match
the single `mailto:<DOCS_ACME_EMAIL>` contact.

## External target configuration

The administrator must create and verify all of the following outside the
repository before enabling either Alibaba Cloud job.

Confirm the existing ICP filing for `githubim.com` remains valid and that the
Alibaba Cloud account has completed the domain-ownership checks required for
mainland acceleration.

### DNS and ownership

1. Preserve the existing GitHub domain-verification TXT record.
2. Create `origin-docs.githubim.com CNAME wukongim.github.io`.
3. Create `acme.docs.githubim.com` as an independent Alibaba Cloud DNS zone.
   Publish that zone's assigned NS delegation in the authoritative
   `githubim.com` zone, then create this exact CNAME in the parent zone:

   ```text
   _acme-challenge.docs.githubim.com
     CNAME _acme-challenge.acme.docs.githubim.com
   ```

   RAM limits record operations to the child zone, and the Workflow further
   fixes the requested domain so lego creates and removes only the resulting
   `_acme-challenge` TXT record. The role has no authority over records in the
   parent `githubim.com` zone.
4. Confirm the Alibaba-assigned CNAME for the configured CDN domain is
   `docs.githubim.com.w.kunlunaq.com`, and configure that exact hostname without
   a trailing dot as `DOCS_CDN_CNAME`. Do not point public DNS to it yet.
5. Keep the public record on its current GitHub Pages target until the cutover
   phase explicitly changes it, and set `DOCS_CDN_PUBLIC_ROUTE_MODE` to
   `github-pages-precutover`.

Do not create a wildcard record for either the origin or ACME validation.

### GitHub Pages

- Keep the repository Pages source set to **GitHub Actions** (`workflow`).
- Before cutover, record the current Pages custom domain and HTTPS state.
- The final custom domain is `origin-docs.githubim.com` with enforced HTTPS.
- GitHub's repository Pages Settings/API is the sole domain authority. The
  static export deliberately contains no `CNAME`; repository content cannot
  safely perform the custom-domain migration.
- Keep `DOCS_SITE_URL=https://docs.githubim.com` so canonical metadata, sitemap
  entries, and reader-facing links continue naming the public domain.

### Alibaba Cloud CDN

Create `docs.githubim.com` in the normal global acceleration product. The
final origin configuration is exact:

```text
origin address: origin-docs.githubim.com:443
origin protocol: HTTPS
origin Host:     origin-docs.githubim.com
origin TLS SNI:  origin-docs.githubim.com
```

Configure HTTP to redirect permanently to HTTPS, enable HTTP/2 and TLS 1.2/1.3,
and disable older TLS versions. Start HSTS at `max-age=86400`; after seven
stable days, raise it to `15552000` (180 days). Do not add
`includeSubDomains` or preload.

Apply cache rules in the following priority order:

| Match | CDN TTL | Notes |
| --- | ---: | --- |
| `/_next/static/**` | 365 days | Content-addressed; never include in routine purge |
| Images and fonts | 30 days | Do not force an equally long browser TTL for mutable paths |
| Non-hashed CSS and JavaScript | 7 days | Preserve origin validators |
| `/api/search` | 10 minutes | No Range origin fetch; verify compressed delivery end to end |
| HTML, route TXT, JSON, XML, and all other routes | 10 minutes | Preserve status, content type, ETag, Last-Modified, and CORS |
| 404 | 1 minute | Do not serve stale for 4xx |
| 5xx | 0 | Never cache a new 5xx response |

Retain query parameters in the cache key by default. The current Next.js export
uses a query token to version at least `/icon.png`, so globally ignoring
parameters would defeat that cache busting and could keep an obsolete image for
the full image TTL. Disable **Ignore Parameters** in
**Performance Optimization > Ignore Parameters** before creating a custom
Cache Key; Alibaba documents the two features as conflicting.

The only approved exception is the static RSC route object generated by the
site's `output: 'export'` build. The post-build gate requires every published
client route to have a non-HTML RSC sibling at `/index.txt`. Create one enabled
rule condition named `docs-static-rsc-index` with `AND` semantics:

| Match type | Operator | Match object or values |
| --- | --- | --- |
| URI (path) | Contains any | `/index.txt` and `/*/index.txt` |
| Request parameter | Exists | request parameter `_rsc` exists |

Then create one **Custom Cache Key** rule that references
`docs-static-rsc-index`, leaves URI rewriting, HTTP Header, and custom variables
empty, and adds exactly one request-parameter operation: **Delete** only `_rsc`.
Every other query parameter remains in the Cache Key, while the complete query
string and request headers continue to reach the origin unchanged. Do not use
an unconditional custom Cache Key rule, a `Reserve` operation, or a rule that
normalizes HTML, images, extensionless routes, ordinary text files, or
`__next.*.txt` segment payloads. Keep the route-TXT TTL at ten minutes during
this change so cache-key normalization is the only experimental variable.

The static export includes `cdn-rsc-refresh-urls.txt`, an inventory-only list
with one absolute URL per eligible physical RSC object. The build derives it
from actual `index.html` artifacts, requires every matching `index.txt`
sibling, uses the fixed `https://docs.githubim.com` origin, sorts and de-duplicates
the URLs, and permits at most 500 entries. The current deployment Workflow
does not consume this inventory; neither does the refresh helper. They
intentionally refresh only four stable public URLs, not every deep `/index.txt`.

A documentation deployment can therefore leave a deep RSC object from the
preceding build at an edge for at most one ten-minute route-TXT TTL. Keep old
content-addressed chunks available at the CDN. Wiring the inventory into
refresh or prefetch, granting `cdn:PushObjectCache`, or lengthening this TTL
requires a separate design and authorization. Never compensate with a
full-site directory purge.

This is a deliberately narrow exception to the general Next.js requirement to
vary RSC responses by `_rsc`: GitHub Pages serves one immutable file for each
physical `/index.txt` pathname, and the static-output gate rejects a missing,
empty, HTML, or malformed RSC sibling. If the site stops using static export or
starts depending on request headers, cookies, or search parameters at render
time, remove this exception before deployment. If analytics noise later
becomes material, strip only a separately reviewed allowlist; never ignore all
parameters.

Enable Gzip and Brotli where supported. The uncompressed search index is larger
than Alibaba Cloud's online-compression size limit, so cutover acceptance must
prove that GitHub's compressed response remains compressed and cacheable
through the edge.

On origin timeout or 5xx, allow a previously successful object to be served
stale for at most 24 hours. Do not apply stale serving to 4xx responses. Do not
configure a full-site directory purge or automatic all-node prefetch.

### Edge certificate automation

Install a valid Let's Encrypt certificate for `docs.githubim.com` before
public DNS cutover. `docs-cdn-certificate.yml` checks twice daily and also
supports a manual forced renewal. Normal renewal starts approximately 30 days
before expiry. Failure fails the Workflow and an isolated issues-only notifier
creates or updates one deduplicated certificate-rotation Issue. It escalates an
unresolved failure when 14, 7, and 3 days remain; a verified recovery closes
the Issue. If an edge inspection fails inside the renewal window, a successful
same-run rotation and edge verification count as recovery; otherwise the final
health gate remains red. The certificate job itself does not receive
`issues: write`, and the notifier receives no Alibaba Cloud or ACME credential.

Each check first records the exact Alibaba certificate fingerprint, expiry,
and provider `DomainCnameStatus`. That provider field is validated and retained
for API diagnostics only; it is not proof of the public DNS route and may be
`ok` before public DNS points to Alibaba Cloud CDN.

The public route is instead selected by `DOCS_CDN_PUBLIC_ROUTE_MODE` and
verified through direct CNAME queries to `223.5.5.5`, `1.1.1.1`, and `8.8.8.8`.
In `github-pages-precutover` mode, all three answers must be
`wukongim.github.io`; the check records
`skipped-public-dns-not-on-alibaba-cdn` and leaves the Alibaba certificate and
expiry checks mandatory. In `alibaba-cdn` mode, all three answers must match
`DOCS_CDN_CNAME`; the public endpoint must then complete a trusted TLS
handshake for `docs.githubim.com` and serve the exact Alibaba API leaf
fingerprint. An empty, split, or unexpected direct CNAME answer, or a hostname,
chain, or fingerprint mismatch, fails the Workflow even when renewal is not
yet due.

The Workflow updates only the CDN edge certificate. It cannot repair the
GitHub Pages origin certificate, DNS, CDN origin configuration, RAM policy, or
OIDC trust. Keep a manual dispatch path for recovery, but investigate repeated
failure rather than repeatedly forcing ACME issuance.

## Preflight while disabled

1. Confirm `DOCS_CDN_ENABLED` is absent or not `true`.
2. Capture the current DNS answers and TTL, GitHub Pages custom domain and HTTPS
   status, active edge certificate, and exported CDN configuration. Store this
   operator-owned snapshot outside the repository.
3. Verify both RAM role trust policies and effective permissions with
   short-lived OIDC sessions. A refresh session must not update certificates or
   DNS; a certificate session must not purge arbitrary domains.
4. Verify the ACME challenge CNAME and delegated target publicly from more than
   one resolver.
5. Configure the CDN origin and cache policy without changing public DNS.
6. Set every Variable and the ACME Environment Secret. Set
   `DOCS_CDN_PUBLIC_ROUTE_MODE=github-pages-precutover`, confirm each fixed
   public resolver directly answers `wukongim.github.io`, then set
   `DOCS_CDN_ENABLED=true`.
7. Manually run `docs-cdn-certificate.yml` with forced renewal. Before public
   DNS points to Alibaba Cloud, the Workflow must report exact API readback,
   three GitHub Pages direct CNAME answers, and
   `Public edge verification: skipped-public-dns-not-on-alibaba-cdn`; this is
   expected, not complete edge acceptance, regardless of the provider's
   `DomainCnameStatus`. Verify the installed certificate's SAN, issuer,
   validity window, and trusted chain through the assigned CDN CNAME or an
   operator-local host override. After public cutover, every scheduled
   inspection and rotation must report `Public edge verification: passed`.
8. Manually run `docs-pages.yml`. GitHub Pages deployment must succeed, and the
   post-deploy refresh must submit exactly these four file URLs in one bounded
   request:

   ```text
   https://docs.githubim.com/
   https://docs.githubim.com/zh/
   https://docs.githubim.com/en/
   https://docs.githubim.com/api/search
   ```

   The refresh job performs no directory purge or prefetch. If authentication
   or refresh fails, the Workflow is red, but the already deployed Pages
   artifact remains live and is not rolled back.
9. Exercise the CDN endpoint through its assigned CNAME or an operator-local
   host override. Do not change public DNS merely to test it.

If any preflight step fails, restore `DOCS_CDN_ENABLED` to a disabled value,
fix the external setup, and repeat preflight. Do not proceed partially.

## Staged cutover

Pause documentation publication for the cutover and work during a low-traffic
window.

Changing the Pages custom domain is a control-plane update, not a content
deployment. A successful REST `204`, `status: built`, or an approved TLS
certificate does not prove that the new Host serves the site. GitHub documents
that changing a routing path does not initiate a rebuild. Every forward or
rollback custom-domain change must therefore be paired with a fresh deployment
and a direct content GET gate.

1. While GitHub Pages still owns `docs.githubim.com`, use this temporary CDN
   migration bridge:

   ```text
   origin connection and SNI: wukongim.github.io
   origin Host:               docs.githubim.com
   ```

   This is a migration aid, not an accepted steady state. Validate it before
   changing public DNS.
2. Warm only `/`, `/zh/`, `/en/`, `/api/search`, and a small reviewed set of
   high-traffic pages. Do not warm or purge the complete export.
3. Change the public record from its GitHub Pages target to:

   ```text
   docs.githubim.com CNAME docs.githubim.com.w.kunlunaq.com
   ```

4. Wait at least twice the previously observed DNS TTL. Validate both the old
   and new resolution paths during propagation. Do not change the route mode
   while any required resolver still returns the old target.
5. After `223.5.5.5`, `1.1.1.1`, and `8.8.8.8` all return
   `DOCS_CDN_CNAME` as the direct CNAME answer, set
   `DOCS_CDN_PUBLIC_ROUTE_MODE=alibaba-cdn` and manually run
   `docs-cdn-certificate.yml` without forced renewal. Require
   `Public edge verification: passed` before continuing.
6. Confirm there is no queued or running `docs-pages.yml` run, fetch protected
   `main`, and record its exact SHA. Dispatch the migration mode before changing
   the Pages domain:

   ```bash
   gh workflow run docs-pages.yml \
     --repo WuKongIM/WuKongIM \
     --ref main \
     -f expected_pages_domain=origin-docs.githubim.com
   ```

   Record the resulting run ID and confirm its `headSha` equals the recorded
   protected-`main` SHA. The Workflow verifies and uploads the artifact first;
   its deploy job then waits for the exact requested Pages domain. A non-empty
   `expected_pages_domain` also suppresses CDN refresh for this staging run.
7. Wait until that run's build job is successful and the deploy job is visibly
   in `Wait for the expected GitHub Pages custom domain`. Do not change the
   custom domain before the artifact is staged. If the wait step times out,
   leave the current domain unchanged and start a new staging run.
8. Keep the Pages source as GitHub Actions, then make the single domain update.
   The equivalent REST body is exact:

   ```json
   {"cname":"origin-docs.githubim.com"}
   ```

   A `204` confirms only that GitHub accepted the setting. The staged run must
   observe this exact value and immediately deploy its already verified
   artifact.
9. While GitHub issues the origin certificate, use only this bounded temporary
   bridge if required:

   ```text
   origin connection and TLS SNI: wukongim.github.io
   origin Host:                   origin-docs.githubim.com
   ```

10. When the Pages API reports an approved certificate containing
    `origin-docs.githubim.com`, enable Pages HTTPS enforcement. Then wait for the
    staging run with `gh run watch <run-id> --exit-status`. Its independent
    `verify_pages_origin` job requires the API state and a **direct origin GET gate**
    to agree: real GETs for `/`, `/zh/`, `/en/`, a deep page, and
    `/api/search` must return complete expected content over trusted TLS while
    `curl --connect-to` bypasses public CDN DNS. Certificate approval alone is
    never content readiness.
11. Only after that run is green, switch CDN address, Host, and SNI together to
    `origin-docs.githubim.com`. Remove all temporary bridge settings. Validate
    the same paths through both the direct origin and the assigned CDN CNAME.
12. Dispatch `docs-pages.yml` normally with no `expected_pages_domain` input.
    This second exact-`main` deployment repeats the direct origin GET gate and,
    when `DOCS_CDN_ENABLED=true`, performs the bounded four-URL CDN refresh only
    after that gate succeeds.
13. Observe for at least 60 minutes before resuming documentation publication.

Never leave a split Host/SNI bridge as permanent configuration.

## Acceptance gate

Test real GET requests, not only HEAD, from Beijing, Shanghai, Guangzhou,
Chengdu, and at least one overseas location.

For a Cache Key change, wait for configuration propagation and run each probe
as an `A -> B -> A` sequence pinned to the same edge address or persistent
connection. Use unique values that have never been requested before:

- Request one representative `/index.txt` with two fresh `_rsc` values and
  otherwise identical RSC headers. All bodies must be byte-identical; request B
  and the repeated A must report `HIT` after the first fill.
- Repeat against the same `/index.txt` while changing one non-`_rsc` query
  parameter. The two fresh values must create distinct misses, and repeating
  the first value must report `HIT`; this proves other parameters were not
  discarded.
- Repeat the negative control on an image path such as `/icon.png`, changing
  `_rsc` between A and B. The fresh values must remain distinct cache objects,
  and repeated A must report `HIT`.
- Before and after the change, compare representative direct-origin RSC
  variants with different router-state, language, and cookie headers. Require
  status, content type, and body digest to remain identical.

- `/`, `/zh/`, `/en/`, a deep page, hashed static assets, JSON, a missing path,
  and `/api/search` return the expected status and content type.
- A second GET clearly reports a cache hit or increasing `Age` where the CDN
  exposes those signals.
- `/api/search` remains gzip-compressed and an actual documentation search
  succeeds.
- Canonical metadata, sitemap URLs, and redirects stay on
  `https://docs.githubim.com`; no response redirects readers to the origin.
- `DOCS_CDN_PUBLIC_ROUTE_MODE` is `alibaba-cdn`, and all three fixed public
  resolvers return `DOCS_CDN_CNAME` as the direct CNAME answer.
- There is no redirect loop, TLS error, unexpected 404, or mixed content.
- Mainland warm-cache p95 TTFB is at most 500 ms.
- Overseas latency does not show a sustained regression greater than 20%
  relative to the captured baseline.
- Sustained TLS or functional failure in any region, or 5xx above 1%, is an
  immediate no-go. Cache-hit ratio is observed but is not a cutover-day rollback
  threshold.

## Rollback

Stop changing other controls as soon as a rollback condition is met.

| Cutover point | Recovery action |
| --- | --- |
| Before public DNS changes | Disable `DOCS_CDN_ENABLED`; remove or disable only the unadvertised CDN configuration |
| Public DNS points to CDN, Pages still owns `docs.githubim.com` | Set `DOCS_CDN_PUBLIC_ROUTE_MODE=github-pages-precutover`, restore the captured public DNS record to `wukongim.github.io`, and wait for all three direct CNAME answers to converge |
| Pages custom domain is migrating | Stage a migration-mode Workflow for the captured domain, restore the captured Pages custom domain, redeploy the exact known-good Pages artifact, pass the direct origin GET gate, then restore the matching temporary origin settings and perform only the bounded refresh |
| Final topology, CDN configuration fault | Restore the exported CDN configuration snapshot; leave DNS stable |
| Alibaba Cloud CDN provider-wide failure | Publish `https://origin-docs.githubim.com` as the direct emergency URL and recover the public domain deliberately; no minute-scale RTO is promised |

Do not attempt final-state rollback by making `docs.githubim.com` a CNAME to
`origin-docs.githubim.com`. GitHub Pages routes by the HTTP Host, so that record
alone does not restore the old custom-domain binding and may return a 404 or
the wrong site.

For a rollback after any Pages-domain mutation, first dispatch
`docs-pages.yml` with `expected_pages_domain=docs.githubim.com` and wait for its
build to finish. Restore the captured Pages custom domain only after that
artifact is staged. The Workflow must then redeploy the exact known-good Pages
artifact and pass its direct origin GET gate before an operator restores or
refreshes the old CDN bridge. A REST `204`, `status: built`, certificate
approval, or a cache refresh cannot replace this deployment-and-GET sequence.

If the Cache Key canary fails, stop the experiment without changing TTL, DNS,
origin, compression, or stale-serving settings. Restore the exported CDN
configuration snapshot, including the previous Ignore Parameters, Custom Cache
Key, and rule-condition state, then wait for propagation and repeat the same
positive and negative `A -> B -> A` probes. Do not approximate the previous
state from the console form.

## Seven-day review

After seven representative days:

1. Review request and byte hit ratios, origin latency, 5xx, traffic, peak
   bandwidth, HTTPS request count, and certificate checks.
2. Set alerts near twice the observed normal peak. Consider a hard cap near
   three times that peak only after confirming the provider's shutdown and
   recovery behavior.
3. Verify at least one scheduled certificate check, one certificate recovery
   Issue test, one Pages deployment, and one CDN refresh receipt.
4. Raise HSTS to 180 days only if origin, edge, renewal, and rollback behavior
   are all stable.
5. Decide from observed hit and origin latency whether HTML or `/api/search`
   should exceed the initial ten-minute TTL. Do not change both while diagnosing
   the same baseline.
