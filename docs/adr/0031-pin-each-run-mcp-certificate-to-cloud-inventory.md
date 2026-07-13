---
status: accepted
---

# Pin each run MCP certificate to cloud inventory

Provisioning will generate a self-signed TLS certificate unique to the Simulation Run with the simulator's public address in its subject alternatives, install only its private key on that simulator, and record the certificate fingerprint in cloud tags that the Analyzer Role cannot modify. Analysis reads the expected fingerprint from provider inventory, verifies and pins the presented certificate without disabling TLS validation, and then exchanges a GitHub OIDC token whose audience includes the exact Run Identity. The gateway must validate issuer, repository, trusted branch, workflow reference, protected environment, run identity, and expiry. Any certificate, claim, or resource-identity mismatch fails closed and removes temporary ingress.
