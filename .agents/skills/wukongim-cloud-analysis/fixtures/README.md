# Analysis Skill Fixtures

These eight fixtures exercise the terminal verdict vocabulary plus the explicit
worker-status-mismatch, terminal-stop-failure, and process-loss-without-root-cause routes of the Skill.
They are deliberately small, ordered MCP transcripts rather than archived run
evidence. `scripts/cloud_analysis_skill_fixtures_test.go` validates their schema,
Run Identity binding, first-tool gate, closed five-value verdict vocabulary and
coverage, plus structured worker-failure routes. Multiple transcripts may cover
the same valid verdict.

The `worker_stop_failed` transcript deliberately stops after cluster state and
bounded simulator headroom. The Analysis MCP has no simulator process/control
log source, so that missing continuity evidence keeps the verdict
`insufficient_evidence` instead of fabricating a simulator observation.

Forward testing should present each transcript to an agent using the Skill and
confirm that the returned verdict matches `expected_verdict`, while allowing
the explanation and confidence to vary with reasoning.
