This directory is used by scripts/start-wukongimv2-single-node.sh to stage
Prometheus binaries before building cmd/wukongimv2.

Generated files are named prometheus-<goos>-<goarch> and are embedded into the
final wukongimv2 binary through go:embed. They are intentionally ignored by git.
