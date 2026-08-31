# Native deployment documentation validation

Date: 2026-08-31 (Asia/Shanghai)

## Scope

This validation exercised the non-Kubernetes server deployment paths published
under `docs-site/content/docs/server/deployment`:

- repository three-node Docker Compose;
- a single-node cluster in one Docker container;
- a single-node cluster supervised by systemd;
- a three-host static cluster supervised by systemd.

Kubernetes was explicitly excluded. The source revision was
`7895be06fa3e7338d51b88d3422208d720853fd1`. The Linux amd64 server artifact
used by the native-host tests had SHA-256
`6288b211b9887300625fb1258484e8f6cb5b42fd747b135f3a364de64cdbb03d`.

## Environment

The final test Lease contained four Alibaba Cloud ECS hosts in `cn-hangzhou`:
three service hosts and one load/build host. Every host ran Ubuntu 24.04 on
x86-64 with a 40 GB system disk and a separate 40 GB data disk. Docker data
on the build host and WuKongIM data on each service host were placed on their
separate ext4 data disks.

The final seven-hour Lease quote was CNY 33.9368 at the workflow's worst-case
rate. Two earlier short-lived Leases were released immediately after their SSH
source-CIDR assumptions proved incorrect; both release receipts reported zero
inventory. The final Lease was also released after the tests described here.

## Results

| Path | Result | Evidence |
| --- | --- | --- |
| Default cold Docker build on this Alibaba network | Blocked, documented | Direct Docker Hub access timed out. `GOPROXY=https://goproxy.cn,direct` later fell back to unreachable Google addresses. |
| Reviewed-mirror Docker build | Pass | With buildx, reviewed image-source overrides, and a complete Alibaba Go proxy, dependency download took 66.7 seconds and all four binaries compiled in 95.6 seconds. Image ID: `sha256:12e57eedc647fc27c1f1b705951bd0d62a15ec70c9f4837c3cca832cea41c062`. |
| Repository three-node Compose before the fix | Fail | All three WuKongIM nodes became ready, but Prometheus and Grafana restarted because fresh-checkout bind directories were created as `root:root` mode `0755` and were not writable by the images' non-root users. |
| Repository three-node Compose after the fix | Pass | Named volumes kept all five services `Up`. Three `/readyz` checks returned `{"ready":true}`, Prometheus returned ready, Grafana reported `database: ok`, and all three Prometheus targets were `up`. |
| Compose message path | Pass | Twenty WKProto clients were online across five person and two group channels. The bounded run sent 56 messages with zero send and receive errors. |
| Single-node container | Pass | The documented read-only config mount, named data volume, runtime tmpfs, network, restart policy, and host port boundaries worked. A marker survived container deletion and recreation, proving volume persistence. |
| Single-node systemd | Pass | The service was enabled and active with zero restarts and `LimitNOFILE=1048576`. It became ready in 3.872 seconds, returned reachable route addresses, and used the dedicated ext4 data disk. Config permissions were `root:wukongim 0640`. |
| Three-host systemd cluster | Pass | Three unique nodes ran the same binary digest from independent ext4 disks. All three readiness and route checks passed. Thirty WKProto clients used nine person and three group channels; 108 messages completed with zero send or receive errors before fault injection. |
| One-node stop and recovery | Boundary confirmed | With three eligible nodes and three replicas, stopping node 3 made nodes 1 and 2 return `503`: `channel placement candidates 2 below replica count 3`. The test workload observed 11 send and 5 receive errors. After node 3 restarted, all three nodes returned `200` and `{"ready":true}` again. |

## Corrective changes

1. Prometheus and Grafana data now use Docker named volumes instead of absent,
   root-created bind directories.
2. The Dockerfile exposes `GOPROXY` as a build argument alongside the existing
   build-image arguments. The Docker guide now requires Compose v2 and buildx,
   documents reviewed registry/module mirrors, and verifies Prometheus and
   Grafana health as well as node readiness.
3. Docker and Linux guides now move every writable plugin path into the
   persistent data directory while keeping only the Unix socket under `/run`.
   This is required because the systemd unit has no `WorkingDirectory`.
4. The multi-node guide now states that a three-node, three-replica topology has
   no candidate-node headroom: a surviving Raft majority does not make the
   remaining nodes ready for the full new-write workload.

## Local verification

- `docker compose config --quiet`
- `GOWORK=off go test ./scripts/... -run 'TestDevSimCompose|TestDockerCompose|TestDockerfile' -count=1`
- `GOWORK=off go test ./scripts/... -count=1`
- `bun test ./lib/native-deployment-contract.test.ts`
- `bun run verify` in `docs-site` (163 tests, 819 static pages, type check,
  lint, build, and static-output contract all passed)

## Cloud cleanup

Final release workflow run:
[33380548555](https://github.com/WuKongIM/WuKongIM/actions/runs/33380548555).
Its release receipt reported `zero_inventory` for disk attachments, disks, EIP
associations, EIPs, ENIs, instances, NAT gateways, route entries, security-group
rules, security groups, VPCs, and VSwitches for Lease
`docs-deploy-20260831T081149Z-lease-3`.
