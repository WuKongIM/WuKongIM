# Review Agent Verification Flow

`internal/runtime/reviewagentverify` owns complete changed-file inventory,
base/control-tree instruction freezing, deterministic named-check planning,
credential-free command execution, append-only evidence, and final evidence
validation. It contains no GitHub API, lifecycle, projection, or model logic.

```text
complete exact test-merge inventory + protected path policy
  -> mandatory named checks

exact base/control tree + changed paths
  -> frozen applicable AGENTS.md / FLOW.md blobs

fixed named check catalog + credential-free per-command disposable worktree
  -> enter the pre-built private-network namespace before Git checkout or command
  -> read-only root/PID filesystem sandbox
     (only disposable worktree + dedicated HOME/TMP writable)
  -> frozen review-agent-check helper for composite checks
  -> bounded process results
  -> append-only ledger outside model workspace
  -> trusted ReviewEvidence

complete ReviewResult + trusted ReviewEvidence + immutable tree digest
  -> validated decision or fail-closed inconclusive
```

No caller may supply a command, working directory, environment override, URL,
test pattern, ref, or repository path to the runner. Only protected catalog
names cross the Check MCP boundary.
