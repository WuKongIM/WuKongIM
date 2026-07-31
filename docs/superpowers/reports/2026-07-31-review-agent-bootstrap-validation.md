# Review Agent Bootstrap Validation

Date: 2026-07-31

This change is the first same-repository validation case for the Review Agent.
It verifies that a ready pull request targeting `main` enters the event-driven
review lifecycle after the direct replacement of the former label-based PR
validation protocol.

The validation succeeds only when the protected controller:

- binds the generation to the exact pull request head and test-merge commit;
- runs the configured deterministic documentation check;
- uses one ephemeral Kimi 3 review session without repository write access;
- publishes its formal Review and status comment through the Review Agent App;
- writes signed state through the separate State Writer App; and
- reports `Review Agent Verdict` from the Review Agent App.

This document changes no product behavior, workflow, policy, permission, or
control-plane path. The pull request remains subject to the same human merge
authority as any other repository change.
