# Backup E2E Rules

This domain proves scheduled full backup and current-cluster restore only
through real `cmd/wukongim` processes, Manager HTTP, and business HTTP APIs.

- Keep Manager authentication enabled and exercise the same permissions,
  password reauthentication, and typed confirmations required in production.
- Treat a one-node deployment as a single-node cluster and keep the production
  256 Hash Slot topology.
- Do not read or mutate node data directories to decide whether backup or
  restore succeeded.
- Assert restored business state through public message APIs.
