# Review Agent CLI Flow

`internal/access/reviewagentcli` is a strict JSON-only process boundary. It
accepts exactly one protected command name and one bounded JSON document on
stdin, then emits exactly one JSON document on stdout.

```text
GitHub Actions job
  -> exact command + strict JSON request
  -> internal/app Review Agent operation
  -> one bounded JSON response
```

The CLI does not parse shell plans, arbitrary commands, paths, URLs, secrets,
or model instructions. Errors are generic and never echo input or credential
material.
