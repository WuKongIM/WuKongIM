# Fake Cloud Deployment Fleet Flow

The fake Fleet implements only the provider-free SSH/native-host port owned by
`internal/usecase/clouddeploy`. It records the load-node staging hop, each
private service-node relay, digest verification, preparation, activation, and
snapshot request in order. Tests inject one exact operation failure or a
bounded readiness snapshot; the adapter never calls Cloud Lease lifecycle APIs,
opens listeners, starts processes, or mutates paid infrastructure.
