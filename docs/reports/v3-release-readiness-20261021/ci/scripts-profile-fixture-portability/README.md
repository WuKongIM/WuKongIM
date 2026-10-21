# Hot-profile fixture portability

The original Linux JSON gate (39565861218) failed because `rg` was absent. The two existing literal assertions now use `grep -Fq`, already present in the shell fixture environment. No product hot-profile behavior, assertion pattern, timing, or threshold changes. The existing focused process test passed with `GOWORK=off GOFLAGS="-p=2 -json"`; see focused.json and focused.log. This is not a complete Linux scripts-gate pass.
