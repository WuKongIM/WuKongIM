# test/e2ev2/control AGENTS

This directory owns black-box e2e scenarios for `cmd/wukongimv2` cluster
control-plane behavior.

## Rules

- Keep scenarios process-level: start real `cmd/wukongimv2` processes through
  `test/e2ev2/suite`.
- Prefer public manager/API HTTP responses and real protocol traffic for
  assertions.
- Do not import `internalv2/app`, `internalv2/usecase`, or storage internals.
- Keep reusable process harness additions in `test/e2ev2/suite`.
