# Cloud Analysis Local Bridge Flow

`wkcloudanalysisbridge` is a local-only transport adapter for one bounded
Analysis session. It listens on an ephemeral IPv4 loopback port and forwards
HTTP MCP requests to one validated HTTPS Analysis endpoint while pinning the
exact server certificate supplied by the authenticated session handoff.

```text
local Codex MCP client
  -> ephemeral http://127.0.0.1:<port>
  -> exact certificate fingerprint and IP-SAN verification
  -> fixed https://<public-ip>:19092|19444 Analysis endpoint
```

The bridge accepts no remote listen address, arbitrary hostname, redirect, or
credential argument. Authorization remains in the forwarded HTTP header. The
operator script owns its lifecycle and terminates it before deleting the local
session directory.
