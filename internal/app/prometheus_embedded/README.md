This directory is used by scripts/start-wukongim-single-node.sh to stage
Prometheus binaries before building cmd/wukongim.

Generated files are named prometheus-<goos>-<goarch> and are embedded into the
final wukongim binary through go:embed. They are intentionally ignored by git.
