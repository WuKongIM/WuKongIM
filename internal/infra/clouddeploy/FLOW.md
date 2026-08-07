# Cloud Deployment Filesystem Adapter Flow

`internal/infra/clouddeploy` implements the `internal/usecase/clouddeploy`
directory port for an existing local bundle root. It owns root-anchored
`openat`/`O_NOFOLLOW` access, atomic exact-mode writes, bounded reads, regular
file inventory, and SHA-256 calculation. It contains no topology, workload,
service-template, or cloud-procurement policy.

`internal/infra/clouddeploy/fake` is the deterministic provider-free adapter
for the activation use-case Fleet port. It records bundle staging, load-node
relay, host preparation, activation, and readiness calls so success and stable
failure gates can be tested without SSH, systemd, or paid infrastructure. It
has no Cloud Lease provider or lifecycle capability. Integration-tagged tests
drive the complete controller through this adapter, while the Deployment shell
adapter has a fake-SSH integration harness covering transfer, verification,
preparation, activation, and typed last-gate failures.
