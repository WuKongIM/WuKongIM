# Analysis Skill Fixtures

These five fixtures exercise the terminal verdict vocabulary of the Skill.
They are deliberately small, ordered MCP transcripts rather than archived run
evidence. `scripts/cloud_analysis_skill_fixtures_test.go` validates their schema,
Run Identity binding, first-tool gate, and one-to-one verdict coverage.

Forward testing should present each transcript to an agent using the Skill and
confirm that the returned verdict matches `expected_verdict`, while allowing
the explanation and confidence to vary with reasoning.
