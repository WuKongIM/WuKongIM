# Issue Agent CLI Flow

`internal/access/issueagentcli` is the JSON-only command boundary for the
GitHub Actions Issue Agent. It validates command names, bounded input sources,
unknown flags, cancellation, and output encoding, then invokes narrow
composition-root operations. It contains no GitHub, state-machine, model, or
Worker business rules.

```text
explicit command + --input <file|->
  -> bounded strict JSON decode
  -> IssueAgent service port
  -> exactly one JSON value on stdout

generate-checkpoint-key --private-key-file <new path>
  -> exclusive 0600 private-key creation
  -> public key record only on stdout
```

Diagnostics go only to stderr and must not include JSON input, credentials, or
private-key bytes. The command is independent of the WuKongIM server lifecycle.
