# Fake Cloud Provider Flow

The fake adapter implements the complete provider boundary without a cloud SDK.
It creates deterministic inventory for one VPC, subnet, security group, three
private cluster hosts, one simulator host, four independent disks, and one
simulator public address.

Deployment, Analysis, and public Cloud View ingress windows are persisted in
the same fake inventory and are cleared independently or together on destroy.

All resources carry the provider-neutral mandatory tags. Failure injection may
retain partial inventory in `release_pending`, so cancellation and sweeper tests
must discover and destroy it by exact Run Identity. A destroyed run remains as a
local tombstone with `released` state and an empty resource list.
