# Review Agent Manual-Trigger Canary

This temporary documentation-only change verifies the production Review Agent
control boundary after the administrator-command rollout.

Expected behavior:

- opening or updating this pull request does not start a Review Agent Worker;
- an exact `@review-agent review` comment from a repository administrator
  starts one review for the current head; and
- the canary pull request is closed without merging after verification.
