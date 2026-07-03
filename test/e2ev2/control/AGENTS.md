# test/e2ev2/control AGENTS

This directory owns black-box e2e scenarios for `cmd/wukongim` cluster
control-plane behavior.

## Rules

- Keep scenarios process-level: start real `cmd/wukongim` processes through
  `test/e2ev2/suite`.
- Prefer public manager/API HTTP responses and real protocol traffic for
  assertions.
- Do not import `internalv2/app`, `internalv2/usecase`, or storage internals.
- Keep reusable process harness additions in `test/e2ev2/suite`.
