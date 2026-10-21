# Confirmed gap and repair failure inventory (before shipping changes)

The pinned Rocky probe runs the complete native shell from failed d824 verbatim (SHA256 1cba6dddb55981e8c4dd0e60f54e108eb10923d06b33008906275c5895c3cb1d), including actual signed stable bootstrap build/install/ownership/repo-content checks and native RPM verification. Tiny TEST ONLY product packages are used. It leaves all authentication enabled and limits network to container loopback.

A diagnostic observer waits 2.1 seconds immediately before the actual DNF Base._store_persistent_data() call; it does not create, delete or edit the persistence JSON or metadata timestamps. The first command then writes ["wukongim-stable"] to /tmp/client-root/tmp/dnf-cache/expired_repos.json. The second command's effective metadata_expire is -1 but its native explicit expired flag is true; it loads B metadata and downloads signed B. The metadata cache is actually /tmp/dnf-cache/wukongim-stable-5702a8a51b8fa885, distinct from the early installroot persistor path. The previous probe delayed only after the first command exited, missing this persistence boundary.

Changing only the first switch command to include the same repository metadata_expire=-1 while retaining makecache --refresh prevents the real close method from persisting expiry. Under the same controlled delay, second-stage native_expired remains false; metadata stays exact A and the B mirrorlist resolves B-prefix exact-A signed RPM. Fresh-B retains normal parameters and returns B.

This establishes a reproducible defect in the exact shipped transaction sequence. It does not prove that the original CI's unrecorded persistence state had this value or uniquely determine that run's timing/GPG trigger. The first-generation probe remains valid only for its smaller scope and must not be cited as complete native acceptance.

## Shipping failure modes and pre-code tests

- First phase lacks -1 and persists an explicit expired state despite the second phase having -1.
- Initial makecache loses --refresh, ceases to fetch/authenticate fresh A, or is moved after download.
- Second phase loses its -1 or makes a global cacheonly change preventing real B-prefix network delivery.
- The policy leaks to normal/fresh-B/persistent-cache/negative/APT cases or changes bootstrap/signature/CA/other command bytes.
- Phase replacement encounters missing/duplicated/reordered boundaries and silently proceeds.
- Existing exact B-after-A, same-case ordering, completed response, exact A metadata/member/hash/size/source/version checks regress.

Before implementation, update the existing real-validator command-composition test to require both exact phases with -1, explicitly preserve one makecache --refresh, and preserve all other modes byte-for-byte. Existing boundary and request-proof rejection tests remain unchanged. Run this test against d824 and preserve the assertion failure. Then make the narrow helper change and run related/full tests. The native signed Rocky and Alma delay comparison is the actual persistence-boundary regression evidence; the fast contract test binds shipping command generation to the proven strategy. Linux exact-head 49 remains required after review.

Do not delete or edit expired_repos.json, change metadata mtime, hold all HTTP responses at A, choose arbitrary A/B, retry CI, change producer/validator policy, or use global cacheonly.
