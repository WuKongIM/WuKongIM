Stable public certificates are registered, but the bootstrap builder only emits preview packages with shared names and owned files. Add an explicit `--channel stable --control-root` preparation mode that reuses reviewed public roles and existing certificate validation, builds separate `wukongim-archive-keyring-stable` / `wukongim-release-stable` packages, and returns an unpublished preparation schema rejected by current preview publication consumers. Stable RPM configuration uses the planned mirrorlist endpoint without a floating baseurl fallback.

The existing preview layout, `/repo`, manifests, signer, composer, audit/client gates and production workflows remain unchanged. This does not enable stable, sign packages, access private keys, upload or publish anything. The native TEST ONLY workflow gains six real build/coinstallation cases; native `--root` installation uses a network-disabled, read-only container with root plus only `SYS_CHROOT`, scratch package databases and public inputs. It does not disable dependency, file-conflict or signature checks.

Validation:

- Base: packages main `51bfedbe88c78758fa05bfb87b13541b4bca4796` (merged #70).
- Tests first: builder CLI/input cases and native harness/workflow contracts recorded red before implementation. A later input-change probe reproduced post-validation certificate substitution before a narrow re-read fix.
- Related Python suite: 52 tests passed with one existing real-RPM test skipped because native RPM tools are unavailable locally; `tests/contract.sh` passed (four workflow checks).
- Fixed preview DEB and RPM spec hashes match the baseline; default/explicit preview inventory and package bytes match in local fixtures.
- Native six-case CI is pending. Local Docker daemon is unavailable, so mock/native-harness tests are not claimed as native installation success.
- These are unsigned package preparation/install checks. Stable package signatures, complete composition/audit, live mirrorlist, CDN A/B/cache behavior and exact public APT/RPM installation remain separate release gates.
