# Stable bootstrap preparation scope

Base: `51bfedbe88c78758fa05bfb87b13541b4bca4796`, tree `2c4f7d3a9b24a6f67c18b114b8a5c5f1f5f6fa5e` (same as approved #70). Root approved this slice on 2026-10-21.

Existing builder defaults to preview with unchanged four input flags and exact bytes/inventory. Stable uses `--channel stable --control-root <reviewed checkout> --output-dir <new directory>` and derives the bootstrap version, stable role declaration, and fixed public certificate paths from that root. Existing `validate-control.py` functions validate closed control, certificate topology/lifetimes and four-family full/16/8 fingerprint uniqueness. Explicit cert/manifest overrides are rejected in stable mode.

Stable owns `wukongim-archive-keyring-stable`, `wukongim-release-stable`, `/usr/share/keyrings/wukongim-archive-keyring-stable.pgp`, `/etc/apt/sources.list.d/wukongim-stable.sources`, `/etc/pki/rpm-gpg/RPM-GPG-KEY-wukongim-stable`, `/etc/yum.repos.d/wukongim-stable.repo`. Normal APT stable suite and enabled repository configuration remain. RPM uses only `https://packages.githubim.com/rpm/stable/el/9/x86_64/mirrorlist`, from storage-design/design.md section 4; no floating baseurl fallback.

Output is a separate explicit unpublished stable preparation inventory, not the existing publishable preview inventory. Existing composer/receipt/client preview consumers must reject it. No channel enabling, private material, signer invocation, cloud changes, publication workflow changes, new manifest, setup installer or protocol. `/repo` remains preview. Stable public endpoint, signed package, A/B/cache/snapshot verification, whole-channel composition and public exact-version installation remain unimplemented by this slice.

Agreed seams: builder CLI/input validation and actual DEB/RPM content/identity; credential-free native scratch package installation in the existing pinned toolchain, preserving verifiable TEST ONLY JSON. Python source-unit tests are limited to these boundaries and native-harness contracts.

# Failure inventory (before implementation)

- A default/explicit preview build changes installed files, package bytes, canonical inventory or names.
- Stable accepts missing/partial role declaration, substituted/swapped certificate, wrong fingerprint, alias collision or invalid lifetime; override flags bypass reviewed control.
- A channel other than preview/stable, stable enabled control, or existing output is accepted.
- Stable package names or owned files collide with preview; wrong key bytes, scriptlets, executable payloads or unexpected file ownership enter the package.
- Stable uses preview suite/key/repo, a floating RPM baseurl or unsigned repository policy.
- A preparation inventory is accepted by the existing preview composer or mistaken for publication.
- Build error leaves output, or reproducibility fails.
- Actual dpkg/rpm install or reinstall changes the other channel's configuration, installs a product binary or package script. Mock-only tests cannot establish native compatibility.
- Native drill omits failed-case evidence, leaks scratch artifacts, adds network/secret/production permission, or reports skipped tests as passed.
