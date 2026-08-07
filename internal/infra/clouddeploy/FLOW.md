# Cloud Deployment Filesystem Adapter Flow

`internal/infra/clouddeploy` implements the `internal/usecase/clouddeploy`
directory port for an existing local bundle root. It owns root-anchored
`openat`/`O_NOFOLLOW` access, atomic exact-mode writes, bounded reads, regular
file inventory, and SHA-256 calculation. It contains no topology, workload,
service-template, or cloud-procurement policy.
