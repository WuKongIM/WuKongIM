# Analysis Skill Fixtures

These seven fixtures exercise the terminal verdict vocabulary plus the explicit
worker-status-mismatch and process-loss-without-root-cause routes of the Skill.
They are deliberately small, ordered MCP transcripts rather than archived run
evidence. `scripts/cloud_analysis_skill_fixtures_test.go` validates their schema,
Run Identity binding, first-tool gate, closed five-value verdict vocabulary and
coverage, plus structured worker-failure routes. Multiple transcripts may cover
the same valid verdict.

Forward testing should present each transcript to an agent using the Skill and
confirm that the returned verdict matches `expected_verdict`, while allowing
the explanation and confidence to vary with reasoning.
