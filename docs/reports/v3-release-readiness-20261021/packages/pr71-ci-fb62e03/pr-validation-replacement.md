Validation completed on first-attempt CI for `fb62e03bb8ceaf300610becdc1f23ff41558c675` (synthetic checkout has the identical tree):

- [Pages](https://github.com/WuKongIM/packages/actions/runs/39572187767): 410 Python tests and four workflow contracts passed with zero skips; all publication steps/jobs skipped.
- [Native](https://github.com/WuKongIM/packages/actions/runs/39572188094): seven metadata, ten local-signing and six new bootstrap cases all passed, followed by the complete trusted-tool signed-repository drill and tracked-tree check. Both channels build reproducibly, and real dpkg/rpm scratch installs pass in both orders with reinstalls; scratch cleanup is confirmed.
- All three small JSON artifact ZIP SHA-256 values match the API and all member CRC/SHA checks pass. Bootstrap outputs remain unsigned, unpublished preparations; production signing, stable composition/audit, live mirrorlist/CDN behavior and public exact-version client acceptance remain separate gates.
