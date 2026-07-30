# Issue Agent CLI Flow

`internal/access/issueagentcli` is a strict JSON-only process boundary. It
accepts exactly seven commands:

```text
reconcile-github
recover-task
build-context
capture-candidate
verify-candidate
mint-app-token
publish-candidate
```

Each command takes one bounded JSON object from stdin or `--input <file>` and
emits one JSON value. Unknown fields, commands, flags, trailing data, and
oversized input fail closed. Diagnostics are generic stderr messages and never
echo input or credentials. Business logic remains in usecase/runtime/infra;
the CLI only validates and dispatches.
