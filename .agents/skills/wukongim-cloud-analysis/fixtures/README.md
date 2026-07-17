# Analysis Skill Fixtures

These five fixtures exercise the terminal verdict vocabulary of the Skill.
They are deliberately small, ordered MCP transcripts rather than archived run
evidence. `scripts/cloud_analysis_skill_fixtures_test.go` validates their schema,
Run Identity binding, first-tool gate, one-to-one verdict coverage, and the
structured worker-failure route used by the scenario-invalid transcript.

Forward testing should present each transcript to an agent using the Skill and
confirm that the returned verdict matches `expected_verdict`, while allowing
the explanation and confidence to vary with reasoning.
