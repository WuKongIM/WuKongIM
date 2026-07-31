# Review Agent Check MCP Flow

`internal/access/reviewagentcheckmcp` is a local stdio MCP adapter over the
trusted named-check runner.

```text
check_list
  -> sorted protected catalog names

check_run(name)
  -> exact catalog lookup
  -> enter the pre-built private-network namespace before disposable checkout
  -> bounded credential-free runner
  -> append-only external evidence ledger

check_result(name)
  -> latest trusted ledger result for the exact generation
```

The stdio adapter itself starts on the credential-free trusted host so Codex
can complete its required MCP handshake. Only a resolved protected check
enters the pre-built private-network namespace. The adapter accepts no command,
argument list, path, environment, URL, ref, test pattern, output location, or
network selector. It exposes no Resources, Prompts, Sampling, Roots, GitHub,
state, or publication operation.
