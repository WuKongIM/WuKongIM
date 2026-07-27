# Goroutine Monitor Scenario

This scenario proves the public Manager realtime-monitor endpoint can fan out
current goroutine ownership across a real three-node `cmd/wukongim` cluster.

Keep assertions black-box. Use only Manager HTTP payloads and do not import
internal app, usecase, registry, or storage packages.
